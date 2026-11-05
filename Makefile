# Commandes utiles. Nécessite Go (et Node seulement pour reconstruire l'interface).

.PHONY: build test ui run install deb iso

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

# Installe / met à jour le service systemd (à lancer en root, après `make build`).
install: build
	./deploy/install.sh

# Paquet Debian (amd64) : dist/efblock_VERSION_amd64.deb
deb:
	packaging/build-deb.sh

# ISO d'installation : dist/efblock-VERSION-amd64.iso (télécharge l'ISO Debian de base ; xorriso requis)
iso:
	image/build.sh
