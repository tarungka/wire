# WIP-04 compatibility notes

Sources without an explicit watermark strategy now use bounded out-of-orderness
with a five-second tolerance. Their legacy `GenerateWatermark()` method remains
in the source interface for compilation compatibility, but the runtime no longer
uses its result. Custom sources relying on that method must configure the desired
watermark strategy explicitly; review event-time behavior before upgrading.

An explicitly configured zero tolerance remains monotonic. Source tasks generate
watermarks at the default 200ms interval through the operator chain and network,
including jobs without event-time windows. Account for this control traffic when
comparing throughput with earlier versions.

The embedded router serializes data sends per destination, so a record blocked by
one partition's capacity does not hold the send lock for other partitions.
Watermark broadcasts remain serialized to preserve generation order, and pending
records remain active during capacity waits.

Known follow-ups: configured source idle timeouts currently apply only to the
immediate downstream task; later shuffles use the default idle timeout.
Watermark-only traffic does not reactivate idle inputs. Window `OnWatermark`
errors fail the task rather than using record retry or DLQ policies.
