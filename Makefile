# Commandes utiles. Nécessite Go (et Node seulement pour reconstruire l'interface).

.PHONY: build test ui run

# Compile le binaire (l'interface web est déjà embarquée, voir internal/web/dist).
build:
	go build -o efblock ./cmd/efblock

test:
	go test -race ./...

# Recompile l'interface Next.js puis la copie dans le dossier embarqué par Go.
# À relancer seulement après avoir modifié web-ui/.
ui:
	cd web-ui && npm ci && npm run build
	rm -rf internal/web/dist
	cp -r web-ui/out internal/web/dist

run: build
	./efblock -debug
