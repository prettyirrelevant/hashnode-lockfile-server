# -----------------------------------------------------------------------------
#  Build Stage
# -----------------------------------------------------------------------------
FROM golang:1.21.4-bullseye as build
WORKDIR /opt/app

COPY ./go.mod .
COPY ./go.sum .

RUN go mod download && go mod verify
COPY . .

ENV CGO_ENABLED=0
RUN go build -o /opt/app/lockfile-server ./main.go

# -----------------------------------------------------------------------------
#  Final Stage
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/base

COPY --from=build /opt/app/lockfile-server /opt/app/lockfile-server

CMD ["/opt/app/lockfile-server"]
