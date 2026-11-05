# Contribuer à efBlock

## Les branches

| Branche | Rôle | Qui y écrit |
|---|---|---|
| `main` | **Production.** Ce qui est publié aux utilisateurs. Chaque version (`v1.2.3`) est un tag posé sur `main`. | Seulement par fusion depuis `dev` (ou un correctif urgent) |
| `dev` | **Préproduction / test.** Là où le travail terminé s'assemble avant d'être publié. | Seulement par fusion de branches de travail |
| `feat/…`, `fix/…`, `docs/…`, `chore/…` | **Une branche par tâche.** | Tout le travail, sans exception |

**On ne travaille jamais directement sur `main` ni sur `dev`.**

## Le trajet d'un changement

1. Créer une branche **depuis `dev`**, avec un nom qui dit ce qu'elle fait :
   ```bash
   git switch dev && git pull
   git switch -c feat/regles-horaires
   ```
2. Travailler, commiter, pousser la branche.
3. Ouvrir une *pull request* **vers `dev`**. La CI doit être verte (formatage, `go vet`, tests avec détecteur de concurrence, compilation de l'interface).
4. Une fois fusionnée dans `dev`, tester le résultat. C'est la préproduction.
5. Pour publier : une *pull request* de **`dev` vers `main`**, puis un tag sur `main`.

## Comment fusionner (important)

| Fusion | Méthode GitHub | Pourquoi |
|---|---|---|
| branche de travail vers `dev` | **Squash and merge** | un commit propre par tâche |
| `dev` vers `main` | **Create a merge commit** (jamais « squash ») | `dev` et `main` gardent le même historique ; avec un squash, ils divergent et les promotions suivantes affichent des conflits parasites |

## Publier une version

Seulement depuis `main`, une fois la fusion faite :

```bash
git switch main && git pull
git tag -a v1.0.0 -m "efBlock 1.0.0"
git push origin v1.0.0
```

Le workflow `Release` construit les paquets Debian et l'ISO, puis les publie dans *Releases* avec leur `SHA256SUMS`. Il **refuse** de publier un tag qui n'est pas sur `main`.
Une version `0.x` est publiée comme « pré-version ».

## Correctif urgent en production

Branche `hotfix/…` créée **depuis `main`**, *pull request* vers `main`, tag, puis la même correction est reportée dans `dev` pour qu'elle ne disparaisse pas à la prochaine version.

## Avant de proposer un changement

```bash
gofmt -l .           # ne doit rien afficher
go vet ./...
go test -race ./...
(cd web-ui && npx tsc --noEmit)
```

Les messages de commit sont rédigés en français et expliquent le **pourquoi** autant que le quoi.
