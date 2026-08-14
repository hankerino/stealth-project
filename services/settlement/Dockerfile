# Settlement Service image.
# Build from the REPO ROOT:
#   docker build -f services/settlement/Dockerfile -t exchange/settlement:dev .
FROM golang:1.22 AS build
WORKDIR /src
COPY services/settlement/go.mod services/settlement/go.sum ./
RUN go mod download
COPY services/settlement/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/settlement .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/settlement /settlement
USER nonroot
EXPOSE 8083
ENTRYPOINT ["/settlement"]
