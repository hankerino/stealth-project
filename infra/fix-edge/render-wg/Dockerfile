# WireGuard jump for the FIX edge: connects OUT to the relay VPS and forwards
# tunnel port 9878 to fix-gateway:9878 on Render's private network.
# Deploy as a Render private service (docker) with env:
#   WG_PRIVATE_KEY, WG_PEER_PUBLIC_KEY, WG_ENDPOINT=<vps ip>:51820
# NOTE: needs NET_ADMIN for a kernel WireGuard interface, which Render does
# not grant; this image therefore uses userspace WireGuard (boringtun) —
# verify it starts on Render before relying on it (untested 2026-09-04).
FROM alpine:3.20
RUN apk add --no-cache wireguard-tools socat boringtun iproute2
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 9878
ENTRYPOINT ["/entrypoint.sh"]
