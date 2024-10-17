# Wire[UNDER DEVELOPMENT]

Wire is an distributed open source stream processing framework with powerful stream and batch-processing capabilities.

---

To build the application you can either `go build` it on you machine or build a docker image with the following code:

```bash
docker build -t wire:1.0.0 .
```

### Usage

---

Edit the `config.json` and add the necessary sources and sinks.

| Attribute Name | Attribute Description                                              | Required |
| -------------- | ------------------------------------------------------------------ | -------- |
| `name`         | A name given to the source/sink for ease of identification.        | No       |
| `type`         | Type of source or sink.                                            | Yes      |
| `key`          | Unique identifier to map the source to the sink.                   | Yes      |
| `config`       | A JSON config of all the attributes to connect to the source/sink. Please refer to the `.config/` folder for more details. | Yes      |

### Currently supported platforms

| Platform      | Source | Sink | Type            |
| ------------- | ------ | ---- | --------------- |
| MongoDB       | ✔️     | ❌   | `mongodb`       |
| Elasticsearch | ❌     | ✔️   | `elasticsearch` |
| Kafka         | ✔️     | ✔️   | `kafka`         |
