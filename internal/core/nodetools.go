package core

import (
	"encoding/json"
	"net/http"
)

// nodesTool serves factory_nodes (node-packaging-updates 3.6): what each node of the company can take on, so a manager
// knows which machine runs which work without being told. Labels the owner set and labels the node detected are
// listed apart; both select runs.
func (c *Core) nodesTool(w http.ResponseWriter, r *http.Request) {
	company := c.companyFrom(r)
	nodes, err := c.Store.Nodes(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	use, err := c.Store.NodesInUse(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	pauses, err := c.Store.Pauses(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	paused := map[string]string{}
	for _, p := range pauses {
		if p.Scope == "node" {
			paused[p.Target] = p.Reason
		}
	}
	type nodeOut struct {
		Name              string   `json:"name"`
		NodeID            string   `json:"node_id"`
		Online            bool     `json:"online"`
		Paused            *string  `json:"paused,omitempty"`
		Disk              string   `json:"disk,omitempty"`
		Slots             int      `json:"slots"`
		SlotsBusy         int      `json:"slots_busy"`
		ConversationSlots int      `json:"conversation_slots"`
		Labels            []string `json:"labels"`
		DetectedLabels    []string `json:"detected_labels"`
		Runtimes          []string `json:"runtimes"`
		Version           string   `json:"version,omitempty"`
		UpdateRequired    bool     `json:"update_required,omitempty"`
	}
	out := make([]nodeOut, 0, len(nodes))
	for _, n := range nodes {
		o := nodeOut{Name: n.Name, NodeID: n.NodeID, Online: n.Online, Slots: n.Slots, ConversationSlots: n.ConversationSlots, Labels: nonEmpty(n.Labels),
			DetectedLabels: nonEmpty(n.DetectedLabels), Runtimes: []string{}, Version: n.Version, UpdateRequired: n.UpdateRequired}
		u := use[n.NodeID]
		o.SlotsBusy = u.Runs - u.ConversationSlots
		if reason, ok := paused[n.NodeID]; ok {
			o.Paused = &reason
		}
		var h struct {
			Disk string `json:"disk"`
		}
		_ = json.Unmarshal(n.Health, &h)
		o.Disk = h.Disk
		for _, d := range n.Drivers {
			if d.OK {
				o.Runtimes = append(o.Runtimes, d.Name)
			}
		}
		out = append(out, o)
	}
	write(w, map[string]any{"nodes": out})
}

func nonEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
