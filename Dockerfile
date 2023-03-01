FROM golang:1.19.5


# Install dependencies
RUN apt-get update && apt-get install -y \
    git \
    curl \
    && rm -rf /var/lib/apt/lists/*


WORKDIR /app

# Copy the local package files to the container's workspace.
COPY *.mod ./
COPY *.sum ./
COPY *.go ./


RUN go mod download

# Install Go tools
RUN go get -d -v

RUN go install -v


RUN go build -o /search

CMD [ "/search" ]

