# Shared multi-stage Dockerfile for every Go service.
# Usage: docker build --build-arg SERVICE=api -f services/Dockerfile services
FROM golang:1.23-alpine AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
# Distroless has no shell/curl: the binary probes itself.
HEALTHCHECK --interval=10s --timeout=4s --start-period=5s --retries=3 CMD ["/app", "-healthcheck"]
ENTRYPOINT ["/app"]
