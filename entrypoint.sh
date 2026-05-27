#!/bin/sh
# kcd entrypoint — fixes volume ownership and creates default config.

for d in /config /state /data /run; do
    [ "$(stat -c %u "$d")" = "0" ] && chown -R 65534:65534 "$d"
done

if [ ! -f /config/kcd/kcd.toml ]; then
    mkdir -p /config/kcd
    cat > /config/kcd/kcd.toml << 'EOF'
log_level = "info"

[plugins]
battery = true
notification = false
mpris = false
systemvolume = false
mousepad = false
EOF
fi

exec "$@"
