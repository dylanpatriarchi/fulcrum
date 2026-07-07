# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/fulcrum ./cmd/fulcrum

# ---- runtime stage ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/fulcrum /fulcrum
COPY config.example.yaml /config.yaml
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/fulcrum"]
CMD ["-config", "/config.yaml"]
