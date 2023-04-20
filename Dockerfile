# Builder stage
FROM golang:1.19-alpine as builder


# Install make
RUN apk update && apk add make

# Set the working directory to /app
WORKDIR /workdir

# Copy the source code from the host to the container
COPY pkg /workdir/app/pkg
COPY cmd /workdir/app/cmd
COPY vendor /workdir/app/vendor
COPY go.mod /workdir/app/go.mod
COPY go.sum /workdir/app/go.sum
COPY Makefile /workdir/app/Makefile

# Set the working directory to /app/pkg/mtq-controller
WORKDIR /workdir/app

RUN make build

# Final stage
FROM golang:1.19-alpine

# Copy the binary from the builder stage to the final image
COPY --from=builder /workdir/app/mtq_controller /app/mtq_controller

# Set the working directory to /app
WORKDIR /app

# Set the entrypoint to the binary
ENTRYPOINT ["/app/mtq_controller"]