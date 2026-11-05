# Consignes pour Claude (assistant de code)

## Flux de branches : à respecter sans exception

- `main` est la **production**, `dev` est la **préproduction / test**. **Ne jamais y commiter ni y pousser directement.**
- Pour **chaque tâche**, créer une nouvelle branche **depuis `dev`** (`feat/…`, `fix/…`, `docs/…`, `chore/…`), y travailler, la pousser, puis ouvrir une
  pull request **vers `dev`** (via `gh api repos/x3Lo/efBlock/pulls`, l'API GraphQL n'est pas disponible).
- **Ne pas fusionner** les pull requests soi-même, sauf demande explicite : c'est le propriétaire qui valide.
- Ne jamais créer de tag ni de release sans demande explicite : une version se publie depuis `main` (voir `CONTRIBUTING.md`).
- Si on est par erreur sur `main` ou `dev`, créer d'abord une branche avant de modifier quoi que ce soit.

## Qualité avant de pousser

`gofmt -l .` (rien), `go vet ./...`, `go test -race ./...`, `cd web-ui && npx tsc --noEmit`. Si `web-ui/` a changé : `make ui` et commiter `internal/web/dist/`.

## Conventions du projet

- Texte, commentaires et messages en français. **Aucun emoji** dans le code, la documentation ni l'interface.
- Commits signés avec l'adresse `noreply` GitHub du propriétaire (jamais son adresse personnelle).
- Les réglages modifiables depuis l'interface passent par `internal/settings` ; les adresses d'écoute et le HTTPS ne se changent que depuis la console (`cmd/efblock-console`).
- Ne jamais coller de jeton, mot de passe ou clé dans un fichier ou un message.
