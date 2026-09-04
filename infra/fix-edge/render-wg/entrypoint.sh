#!/bin/sh
set -eu
: "${WG_PRIVATE_KEY:?}" "${WG_PEER_PUBLIC_KEY:?}" "${WG_ENDPOINT:?}"
mkdir -p /etc/wireguard
cat > /etc/wireguard/wg0.conf <<CONF
[Interface]
Address = 10.77.0.2/24
PrivateKey = ${WG_PRIVATE_KEY}

[Peer]
PublicKey = ${WG_PEER_PUBLIC_KEY}
Endpoint = ${WG_ENDPOINT}
AllowedIPs = 10.77.0.1/32
PersistentKeepalive = 25
CONF
export WG_QUICK_USERSPACE_IMPLEMENTATION=boringtun WG_SUDO=1
wg-quick up wg0
echo "wg up; forwarding 10.77.0.2:9878 -> fix-gateway:9878"
exec socat TCP-LISTEN:9878,bind=10.77.0.2,fork,reuseaddr TCP:fix-gateway:9878
