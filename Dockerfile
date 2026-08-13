# The web assets are embedded in the binary and the SQLite driver is pure Go,
# so the final image needs nothing but the binary itself.
FROM golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first, so a code change does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/raffle ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/raffle /raffle

# The database lives on a mounted volume. Without one, a redeploy takes the
# roster and every draw with it.
VOLUME /data
ENV DB_PATH=/data/diaper-raffle.db
ENV ADDR=:8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/raffle"]
