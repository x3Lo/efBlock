package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

// genSelfSigned crée un certificat HTTPS auto-signé pour les adresses et noms donnés, et
// retourne les chemins du certificat et de la clé.
//
// Auto-signé : personne d'autre que toi ne l'a validé, donc le navigateur affiche un
// avertissement la première fois. Le trafic est quand même chiffré : le mot de passe de
// l'interface ne circule plus en clair sur le réseau.
func genSelfSigned(dir string, ips []net.IP, names []string, valid time.Duration) (certPath, keyPath string, err error) {
	if len(ips) == 0 && len(names) == 0 {
		return "", "", errors.New("aucune adresse ni nom pour le certificat")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "efBlock", Organization: []string{"efBlock"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(valid),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	certPath, keyPath = filepath.Join(dir, "web-cert.pem"), filepath.Join(dir, "web-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o640); err != nil {
		return "", "", err
	}
	// La clé doit être lisible par le service (compte efblock), pas par tout le monde.
	if u, err := user.Lookup("efblock"); err == nil && os.Geteuid() == 0 {
		if gid, err := strconv.Atoi(u.Gid); err == nil {
			_ = os.Chown(keyPath, 0, gid)
		}
	}
	return certPath, keyPath, nil
}
