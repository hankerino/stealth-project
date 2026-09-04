# Catalog Service image.
#
# Build from the REPO ROOT (the build needs nothing outside services/catalog,
# but a uniform repo-root context keeps both services' builds identical):
#   docker build -f services/catalog/Dockerfile -t exchange/catalog:dev .
FROM golang:1.26 AS build
WORKDIR /src
COPY services/catalog/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/catalog .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/catalog /catalog
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/catalog"]
