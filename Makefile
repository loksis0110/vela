TAGS := with_gvisor,with_quic,with_utls,with_clash_api
VERSION := 1.3.2

.PHONY: core test windows android-lib ios-lib android icons

core:
	./scripts/prepare-core.sh
	go mod tidy

test: core
	go test -tags "$(TAGS)" ./core/

# Windows client: dist/Vela.exe (single file, no installer needed)
windows: core
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
