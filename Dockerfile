FROM golang:1.22.5 as builder
WORKDIR /app
COPY . ./

WORKDIR /app
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o server .

# final image
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /

COPY --from=builder /app/server /server

USER nonroot:nonroot
ENTRYPOINT ["/server"]