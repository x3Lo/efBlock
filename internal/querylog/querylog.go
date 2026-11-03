// Package querylog enregistre chaque requête DNS dans une base SQLite
// (domaine, client, bloqué ou non, horodatage) et calcule les statistiques
// du tableau de bord.
package querylog

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pilote SQLite en Go pur : pas de CGO, compilation croisée simple
)

// Ce qu'il est arrivé à une requête.
const (
	ActionBlocked   = "blocked"   // refusée par le filtre
	ActionCached    = "cached"    // servie depuis le cache
	ActionForwarded = "forwarded" // transmise à l'upstream
	ActionError     = "error"     // aucun upstream n'a répondu (SERVFAIL)
)

// Entry est une ligne du journal.
type Entry struct {
	Time     time.Time
	Client   string // IP du client
	Name     string // domaine demandé, minuscules, sans point final
	QType    string // A, AAAA, HTTPS...
	Action   string // une des constantes Action*
	Rule     string // si bloqué : la règle qui a matché
	Rcode    string // NOERROR, NXDOMAIN...
	Duration time.Duration
}

const schema = `
CREATE TABLE IF NOT EXISTS queries (
	id     INTEGER PRIMARY KEY,
	ts     INTEGER NOT NULL,            -- millisecondes Unix
	client TEXT    NOT NULL,
	name   TEXT    NOT NULL,
	qtype  TEXT    NOT NULL,
	action TEXT    NOT NULL,
	rule   TEXT    NOT NULL DEFAULT '',
	rcode  TEXT    NOT NULL DEFAULT '',
	dur_us INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_queries_ts ON queries(ts);
`

const (
	queueSize = 4096 // requêtes en attente d'écriture
	batchSize = 256  // lignes écrites par transaction
)

// Logger écrit le journal de façon asynchrone.
//
// Principe clé : répondre à une requête DNS ne doit JAMAIS attendre le
// disque. Log() dépose simplement l'entrée dans une file (un "channel") et
// retourne immédiatement. Une goroutine séparée vide la file et écrit par
// paquets, dans une seule transaction : des centaines de lignes par écriture
// disque au lieu d'une, bien plus léger pour une petite VM.
// Si la file est pleine (disque très lent), on abandonne l'entrée : mieux
// vaut perdre une ligne de log que ralentir le DNS de toute la maison.
type Logger struct {
	db        *sql.DB
	ch        chan Entry
	done      chan struct{}
	dropped   atomic.Uint64
	retention time.Duration

	mu     sync.RWMutex // protège closed : on n'envoie jamais dans un channel fermé
	closed bool
}

// Open ouvre (ou crée) la base. retention = durée de conservation (0 = illimitée).
func Open(path string, retention time.Duration) (*Logger, error) {
	// Mode WAL : les lectures (dashboard) ne bloquent pas les écritures.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("création du schéma: %w", err)
	}
	l := &Logger{
		db:        db,
		ch:        make(chan Entry, queueSize),
		done:      make(chan struct{}),
		retention: retention,
	}
	l.purge() // nettoie dès le démarrage
	go l.run()
	return l, nil
}

// Log enregistre une requête. Ne bloque jamais. Sans effet sur un Logger nil.
func (l *Logger) Log(e Entry) {
	if l == nil {
		return
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return
	}
	select {
	case l.ch <- e:
	default:
		l.dropped.Add(1) // file pleine
	}
}

// Close vide la file, écrit les dernières lignes et ferme la base.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.ch)
	l.mu.Unlock()
	<-l.done // attend la fin de l'écriture
	if n := l.dropped.Load(); n > 0 {
		slog.Warn("entrées de log abandonnées (file pleine)", "nombre", n)
	}
	return l.db.Close()
}

// run : boucle d'écriture, tourne dans sa propre goroutine.
func (l *Logger) run() {
	defer close(l.done)
	batch := make([]Entry, 0, batchSize)
	flushTick := time.NewTicker(time.Second)
	purgeTick := time.NewTicker(time.Hour)
	defer flushTick.Stop()
	defer purgeTick.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := l.insert(batch); err != nil {
			slog.Warn("écriture du journal", "err", err, "perdues", len(batch))
		}
		batch = batch[:0]
	}
	for {
		select {
		case e, ok := <-l.ch:
			if !ok { // Close() a été appelé : on écrit le reste et on sort
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= batchSize {
				flush()
			}
		case <-flushTick.C:
			flush()
		case <-purgeTick.C:
			l.purge()
		}
	}
}

func (l *Logger) insert(batch []Entry) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO queries (ts, client, name, qtype, action, rule, rcode, dur_us)
	                         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, e := range batch {
		if _, err := stmt.Exec(e.Time.UnixMilli(), e.Client, e.Name, e.QType, e.Action, e.Rule, e.Rcode, e.Duration.Microseconds()); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// purge supprime les lignes plus vieilles que la durée de conservation.
func (l *Logger) purge() {
	if l.retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-l.retention).UnixMilli()
	if _, err := l.db.Exec(`DELETE FROM queries WHERE ts < ?`, cutoff); err != nil {
		slog.Warn("purge du journal", "err", err)
	}
}

// --- Statistiques (lecture) -------------------------------------------------

// Summary : compteurs globaux sur une période.
type Summary struct {
	Total, Blocked, Cached, Forwarded, Errors int64
}

// BlockedPercent : part des requêtes bloquées, en %.
func (s Summary) BlockedPercent() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Blocked) * 100 / float64(s.Total)
}

// Summary compte les requêtes depuis `since`.
func (l *Logger) Summary(since time.Time) (Summary, error) {
	var s Summary
	err := l.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(action = 'blocked'),   0),
		       COALESCE(SUM(action = 'cached'),    0),
		       COALESCE(SUM(action = 'forwarded'), 0),
		       COALESCE(SUM(action = 'error'),     0)
		FROM queries WHERE ts >= ?`, since.UnixMilli()).
		Scan(&s.Total, &s.Blocked, &s.Cached, &s.Forwarded, &s.Errors)
	return s, err
}

// Count : un élément d'un classement (domaine ou client) et son nombre de requêtes.
type Count struct {
	Name  string
	Count int64
}

// TopBlocked : les domaines les plus bloqués.
func (l *Logger) TopBlocked(since time.Time, limit int) ([]Count, error) {
	return l.top(`SELECT name, COUNT(*) FROM queries WHERE ts >= ? AND action = 'blocked'
	              GROUP BY name ORDER BY COUNT(*) DESC, name LIMIT ?`, since, limit)
}

// TopDomains : les domaines les plus demandés (non bloqués).
func (l *Logger) TopDomains(since time.Time, limit int) ([]Count, error) {
	return l.top(`SELECT name, COUNT(*) FROM queries WHERE ts >= ? AND action != 'blocked'
	              GROUP BY name ORDER BY COUNT(*) DESC, name LIMIT ?`, since, limit)
}

// TopClients : les clients les plus bavards.
func (l *Logger) TopClients(since time.Time, limit int) ([]Count, error) {
	return l.top(`SELECT client, COUNT(*) FROM queries WHERE ts >= ?
	              GROUP BY client ORDER BY COUNT(*) DESC, client LIMIT ?`, since, limit)
}

func (l *Logger) top(query string, since time.Time, limit int) ([]Count, error) {
	rows, err := l.db.Query(query, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Recent : les dernières requêtes, de la plus récente à la plus ancienne.
func (l *Logger) Recent(limit int) ([]Entry, error) {
	rows, err := l.db.Query(`SELECT ts, client, name, qtype, action, rule, rcode, dur_us
	                         FROM queries ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ts, dur int64
		if err := rows.Scan(&ts, &e.Client, &e.Name, &e.QType, &e.Action, &e.Rule, &e.Rcode, &dur); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts)
		e.Duration = time.Duration(dur) * time.Microsecond
		out = append(out, e)
	}
	return out, rows.Err()
}
