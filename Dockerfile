# Use a base image with Go
FROM golang:1.17 as go-builder

# Set the working directory for the Go application
WORKDIR /go/src/app

# Copy your Go source code and Go module files
COPY app.go go.mod go.sum ./

# Download Go module dependencies
RUN go mod download

# Build the Go application
RUN GOOS=linux GOARCH=arm64 go build -o app

# Use a Debian-based image for running your application
FROM python

# Install yt-dlp and any other dependencies you need
RUN apt-get -y update && \
    apt-get install -y ffmpeg bash && \
    apt-get -y clean all && \
    python3 -m pip install youtube-dl yt-dlp apprise

# Create a directory for downloads
RUN mkdir /downloads

# Set the working directory
WORKDIR /downloads

# Copy the Go binary from the go-builder stage to the final image
COPY --from=go-builder /go/src/app/app /usr/local/bin/app

# Make the Go binary executable
RUN chmod +x /usr/local/bin/app

# Copy your shell scripts
COPY run-youtube-dl.sh /run-youtube-dl.sh
COPY do-notify.sh /do-notify.sh

# Make your shell scripts executable
RUN chmod +x /run-youtube-dl.sh
RUN chmod +x /do-notify.sh

# Define the command to run your Go application
CMD ["/usr/local/bin/app"]
