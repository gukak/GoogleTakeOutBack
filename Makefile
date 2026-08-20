.PHONY: build build-windows build-all vet test clean checksums release-assets

VERSION ?= v0.6.5
GO      ?= go
LDFLAGS := -s -w -X github.com/gukak/GoogleTakeOutBack/internal/app.Version=$(VERSION)

build:
	cd src && $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o ../TakeOutBack/tools/linux/takeoutback ./cmd/takeoutback

build-windows:
	cd src && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o ../TakeOutBack/tools/windows/takeoutback.exe ./cmd/takeoutback

build-all: build build-windows

vet:
	cd src && $(GO) vet ./...

test:
	cd src && $(GO) test ./...

checksums: build-all
	cd TakeOutBack/tools/linux && sha256sum takeoutback > takeoutback.sha256
	cd TakeOutBack/tools/windows && sha256sum takeoutback.exe > takeoutback.exe.sha256

release-assets: checksums
	mkdir -p dist
	cp TakeOutBack/tools/linux/takeoutback dist/takeoutback-linux-amd64
	cp TakeOutBack/tools/linux/takeoutback.sha256 dist/takeoutback-linux-amd64.sha256
	cp TakeOutBack/tools/windows/takeoutback.exe dist/takeoutback-windows-amd64.exe
	cp TakeOutBack/tools/windows/takeoutback.exe.sha256 dist/takeoutback-windows-amd64.exe.sha256
	cp takeOutBack.sh dist/takeOutBack.sh
	cp takeOutBack.bat dist/takeOutBack.bat
	cp TakeOutBack/config/settings.json dist/settings.json
	cp TakeOutBack/config/policy.json dist/policy.json
	cp README.md dist/README.md

clean:
	rm -f TakeOutBack/tools/linux/takeoutback TakeOutBack/tools/windows/takeoutback.exe
	rm -f TakeOutBack/tools/linux/*.sha256 TakeOutBack/tools/windows/*.sha256
	rm -rf dist
