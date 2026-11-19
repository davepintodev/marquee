FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/marquee ./cmd/marquee

# The static distroless image ships the CA certificates marquee needs for
# https calls to AIOStreams and TMDB; there is no shell in it.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/marquee /marquee
EXPOSE 8080
ENTRYPOINT ["/marquee"]
