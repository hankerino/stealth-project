# ---- build -----------------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api .

# ---- runtime ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
ARG APP_VERSION=dev
ENV APP_VERSION=$APP_VERSION
COPY --from=build /out/api /api
USER nonroot
EXPOSE 8443
ENTRYPOINT ["/api"]
