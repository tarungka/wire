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

RUN go mod download

COPY *.go ./
COPY ./cmd ./cmd
COPY ./sources ./sources
COPY ./sinks ./sinks
COPY ./.config ./.config
COPY ./tests/config.json ./.config/config.json

RUN go build -o /wire ./cmd

CMD [ "/wire" , "--config", "./.config/config.json"]

