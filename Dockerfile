FROM alpine:latest

WORKDIR /app

ARG APP_BINARY=app/build/vinyl-keeper
COPY ${APP_BINARY} /usr/local/bin/vinyl-keeper

RUN chmod +x /usr/local/bin/vinyl-keeper

EXPOSE 8080

CMD ["vinyl-keeper"]
