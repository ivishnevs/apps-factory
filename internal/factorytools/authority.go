package factorytools

// authorityDefinitions are the authority actions over tasks (hierarchy D2), for the owner's managers up an
// assignee's chain and the task's delegator.
func authorityDefinitions() []Tool {
	s := func() any { return map[string]any{"type": "string"} }
	n := map[string]any{"type": "integer"}
	tool := func(name, description string, props map[string]any, required ...string) Tool {
		return Tool{"factory_task_" + name, "Factory: " + description, map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, "factory.task." + name}
	}
	return []Tool{
		tool("pause", "Pause a task you have authority over (id, version) so it gets no new runs, or with agent an agent of your subtree; a running run continues. Not a wake-up: its waits stay.",
			map[string]any{"id": s(), "version": n, "agent": s(), "reason": s(), "request_id": s()}, "request_id"),
		tool("resume", "Resume a task (id, version) or an agent you or a lower authority paused; the task runs again only with new input.",
			map[string]any{"id": s(), "version": n, "agent": s(), "reason": s(), "request_id": s()}, "request_id"),
		tool("reassign", "Give a task you have authority over to an agent of your subtree (or yourself); a running run stops and the new assignee starts with your reason.",
			map[string]any{"id": s(), "version": n, "assignee": s(), "reason": s(), "request_id": s()}, "id", "version", "assignee", "request_id"),
		tool("reprioritize", "Change the priority (-1000..1000) of a task you have authority over; its waits stay.",
			map[string]any{"id": s(), "version": n, "priority": n, "request_id": s()}, "id", "version", "priority", "request_id"),
	}
}
