package rpc

import "fmt"

func (op OperatorDescriptor) HasSideOutput(tag string) bool {
	if tag == "" {
		return false
	}
	if op.Type == OperatorTypeWindow && op.LateOutputTag == tag {
		return true
	}
	if op.Type == OperatorTypeProcess {
		for _, declared := range op.SideOutputTags {
			if declared == tag {
				return true
			}
		}
	}
	return false
}
func (op OperatorDescriptor) ValidateSideOutputs() error {
	if len(op.SideOutputTags) > 0 && op.Type != OperatorTypeProcess {
		return fmt.Errorf("side_output_tags require a Process operator")
	}
	seen := map[string]bool{}
	for _, tag := range op.SideOutputTags {
		if tag == "" || seen[tag] {
			return fmt.Errorf("process side output tags must be nonempty and unique")
		}
		seen[tag] = true
	}
	return nil
}
