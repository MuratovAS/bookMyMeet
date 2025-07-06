FROM golang:1-alpine AS builder

RUN mkdir /work
WORKDIR /work

COPY 	go.mod	.
COPY 	go.sum	.
COPY 	*.go	.

RUN go build -o bookMyMeet

FROM alpine:latest

COPY --from=builder /work/bookMyMeet /app/bookMyMeet
COPY static /app/static

EXPOSE 5000
USER nobody
WORKDIR /app

ENTRYPOINT  ["/app/bookMyMeet"]
