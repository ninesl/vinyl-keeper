FROM golang:1.26.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/vinyl-keeper .

FROM alpine:latest

WORKDIR /app

COPY --from=build /out/vinyl-keeper /usr/local/bin/vinyl-keeper

EXPOSE 8080

CMD ["vinyl-keeper"]
