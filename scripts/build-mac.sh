mkdir -p ./temp_libs
cp "$(brew --prefix opus)/lib/libopus.a" ./temp_libs/

# 2. Build and tell the compiler to look ONLY in that folder for libraries
# We use -L to point to our folder and -lopus to find the only file there
CGO_ENABLED=1 \
CGO_LDFLAGS="-L$(pwd)/temp_libs -lopus" \
go build -tags nolibopusfile \
-ldflags="-linkmode=external -extldflags=-Wl,-sectcreate,__TEXT,__info_plist,$(pwd)/info.plist,-framework,CoreAudio,-framework,AudioToolbox" \
-o kwebbel-mac ./main-cli.go

# 3. Clean up
rm -rf ./temp_libs


codesign --force --options runtime \
  --entitlements entitlements.plist \
  --identifier "com.kwebbel.cli" \
  --sign "-" kwebbel-mac