TAGS := with_gvisor,with_quic,with_utls,with_clash_api
VERSION := 1.4.1

.PHONY: core rules test windows android-lib ios-lib android icons

core:
	./scripts/prepare-core.sh
	go mod tidy

# The Xray end-to-end tests run against a real Xray server built from the
# same xray-core version as the embedded engine (skipped if it can't be built).
XRAY_VERSION = $(shell sed -n 's/^[[:space:]]*github.com\/xtls\/xray-core //p' go.mod)

test: core
	GOBIN="$(CURDIR)/.cache/bin" GOWORK=off go install github.com/xtls/xray-core/main@$(XRAY_VERSION) || true
	@test -x .cache/bin/main && echo "::notice title=Xray tests::end-to-end tests run against $$(.cache/bin/main version | head -n 1)" \
		|| echo "::warning title=Xray tests::the Xray server could not be built, end-to-end tests are skipped"
	VELA_XRAY_BIN="$$(test -x .cache/bin/main && echo "$(CURDIR)/.cache/bin/main")" go test -tags "$(TAGS)" ./core/ ./mobile/

# Fresh "blocked in Russia" lists for release builds
rules: core
	./scripts/update-rules.sh

# Windows client: dist/Vela.exe (single file, no installer needed)
windows: core rules
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags "$(TAGS)" \
		-ldflags "-s -w -H windowsgui -X main.Version=$(VERSION)" -o dist/Vela.exe ./desktop

android-lib:
	./scripts/build-mobile-libs.sh android

android: android-lib
	cd android && ./gradlew assembleRelease

ios-lib:
	./scripts/build-mobile-libs.sh ios

icons:
	python3 tools/make_icons.py
