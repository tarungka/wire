package engine

import "fmt"

const sinkBatchLimit = 100

type sinkBatch struct {
	sink   BatchSinkOperator
	events []Event
}

func configureSinkBatches(links []ChainLink) {
	for i := range links {
		link := &links[i]
		// Record policies need unambiguous per-record outcomes. A batch error
		// cannot distinguish successful writes from poison records.
		if link.Config.MaxRetries > 0 || link.Config.OnExhausted != FailJob || link.Config.Classifier != nil {
			continue
		}
		if sink, ok := link.Operator.(BatchSinkOperator); ok {
			link.batch = &sinkBatch{sink: sink}
		}
	}
}

func (b *sinkBatch) flush(cc *chainContext, link ChainLink) error {
	if len(b.events) == 0 {
		return nil
	}
	if err := invokeLegacyWithMetrics(cc, link, func() error { return b.sink.WriteBatch(cc.ctx, b.events) }); err != nil {
		return fmt.Errorf("batch sink operator: %w", err)
	}
	for _, event := range b.events {
		recordTaskOutput(cc.ctx, event)
	}
	clear(b.events)
	b.events = b.events[:0]
	return nil
}

func (cc *chainContext) flushSinkBatches() error {
	for _, link := range cc.links {
		if link.batch != nil {
			if err := link.batch.flush(cc, link); err != nil {
				return err
			}
		}
	}
	return nil
}
