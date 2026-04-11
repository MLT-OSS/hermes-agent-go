package agent

// fireStreamDelta fires all registered stream callbacks with a text chunk.
func (a *AIAgent) fireStreamDelta(text string) {
	if a.callbacks != nil && a.callbacks.OnStreamDelta != nil {
		a.callbacks.OnStreamDelta(text)
	}
}

// fireReasoning fires the reasoning callback.
func (a *AIAgent) fireReasoning(text string) {
	if a.callbacks != nil && a.callbacks.OnReasoning != nil {
		a.callbacks.OnReasoning(text)
	}
}

// fireToolProgress fires the tool progress callback.
func (a *AIAgent) fireToolProgress(toolName, argsPreview string) {
	if a.callbacks != nil && a.callbacks.OnToolProgress != nil {
		a.callbacks.OnToolProgress(toolName, argsPreview)
	}
}

// fireToolStart fires when a tool starts executing.
func (a *AIAgent) fireToolStart(toolName string) {
	if a.callbacks != nil && a.callbacks.OnToolStart != nil {
		a.callbacks.OnToolStart(toolName)
	}
}

// fireToolComplete fires when a tool completes.
func (a *AIAgent) fireToolComplete(toolName string) {
	if a.callbacks != nil && a.callbacks.OnToolComplete != nil {
		a.callbacks.OnToolComplete(toolName)
	}
}

// fireStep fires on each API step.
func (a *AIAgent) fireStep(iteration int, prevTools []string) {
	if a.callbacks != nil && a.callbacks.OnStep != nil {
		a.callbacks.OnStep(iteration, prevTools)
	}
}

// fireStatus fires status updates.
func (a *AIAgent) fireStatus(msg string) {
	if a.callbacks != nil && a.callbacks.OnStatus != nil {
		a.callbacks.OnStatus(msg)
	}
}

// hasStreamConsumers returns true if any streaming consumer is registered.
func (a *AIAgent) hasStreamConsumers() bool {
	return a.callbacks != nil && a.callbacks.OnStreamDelta != nil
}
