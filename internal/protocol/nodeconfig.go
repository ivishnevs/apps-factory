package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Node settings from the web (node-packaging-updates design part B): Core keeps a versioned desired config per node,
// the poll answer carries its version, the node fetches it, applies what node.yaml lets it (pins, slots_max,
// policy.runtimes), keeps it in <state_dir>/remote-config.json over node.yaml and reports the outcome per setting in
// its heartbeat. Only capacity and scheduling settings are remote; trust and machine settings stay in node.yaml.

// NodeSettings are the remote settings; an absent field leaves node.yaml's value.
type NodeSettings struct {
	Slots             *int                          `json:"slots,omitempty"`
	ConversationSlots *int                          `json:"conversation_slots,omitempty"`
	Labels            *[]string                     `json:"labels,omitempty"`
	Drivers           map[string]NodeDriverSettings `json:"drivers,omitempty"`
	Disk              *NodeDiskSettings             `json:"disk,omitempty"`
}

// NodeDriverSettings: whether the node runs the driver (drivers.<name>.enabled).
type NodeDriverSettings struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// NodeDiskSettings are the disk watermarks in GiB (node.yaml disk); an absent one keeps node.yaml's.
type NodeDiskSettings struct {
	LowGiB               *float64 `json:"low_watermark_gib,omitempty"`
	CriticalGiB          *float64 `json:"critical_watermark_gib,omitempty"`
	ConversationFloorGiB *float64 `json:"conversation_floor_gib,omitempty"`
}

// Remote setting keys: what a report names and node.yaml remote: pins.
const (
	SettingSlots             = "slots"
	SettingConversationSlots = "conversation_slots"
	SettingLabels            = "labels"
	SettingDrivers           = "drivers"
	SettingDisk              = "disk"
)

// SettingKeys are the remote setting keys in display order; a driver's key in a report is drivers.<name>.
var SettingKeys = []string{SettingSlots, SettingConversationSlots, SettingLabels, SettingDrivers, SettingDisk}

// RemoteDrivers are the drivers a remote config may name.
var RemoteDrivers = []string{"codex", "claude"}

// MaxNodeSlots bounds slots and conversation_slots (as node.yaml and register).
const MaxNodeSlots = 64

// Setting states of a report; Core adds pending for a desired version the node has not applied yet.
const (
	SettingApplied = "applied"
	SettingRefused = "refused"
	SettingPending = "pending"
)

// NodeSettingState is one setting's outcome on the node: applied or refused with the reason.
type NodeSettingState struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// NodeConfigReport is what the node reports in its heartbeat: the desired version it applied (0: none), each
// setting of that version's outcome (keys: slots, conversation_slots, labels, drivers.<name>, disk), node.yaml's own
// values of the remote settings, the keys node.yaml pins and its slots_max.
type NodeConfigReport struct {
	Version  int64                       `json:"version"`
	Settings map[string]NodeSettingState `json:"settings"`
	Local    NodeSettings                `json:"local"`
	Pinned   []string                    `json:"pinned,omitempty"`
	SlotsMax int                         `json:"slots_max,omitempty"`
}

// NodeConfig is GET /nodes/{id}/config for the node: the desired settings and their version.
type NodeConfig struct {
	Version int64        `json:"version"`
	Config  NodeSettings `json:"config"`
}

var nodeLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidLabel reports whether l is a node label: letters, digits, '.', '_' or '-', at most 63.
func ValidLabel(l string) bool { return nodeLabel.MatchString(l) }

// Validate checks every setting as the node would; Core refuses an invalid config before storing it.
func (s NodeSettings) Validate() error {
	var errs []error
	if s.Slots != nil && (*s.Slots < 1 || *s.Slots > MaxNodeSlots) {
		errs = append(errs, fmt.Errorf("slots %d: must be between 1 and %d", *s.Slots, MaxNodeSlots))
	}
	if s.ConversationSlots != nil && (*s.ConversationSlots < 0 || *s.ConversationSlots > MaxNodeSlots) {
		errs = append(errs, fmt.Errorf("conversation_slots %d: must be between 0 and %d", *s.ConversationSlots, MaxNodeSlots))
	}
	if s.Labels != nil {
		if len(*s.Labels) > 32 {
			errs = append(errs, errors.New("labels: at most 32"))
		}
		for _, l := range *s.Labels {
			if !ValidLabel(l) {
				errs = append(errs, fmt.Errorf("label %q: letters, digits, '.', '_' or '-', at most 63", l))
			}
		}
	}
	for name := range s.Drivers {
		if !slices.Contains(RemoteDrivers, name) {
			errs = append(errs, fmt.Errorf("drivers.%s: unknown driver (codex or claude)", name))
		}
	}
	if d := s.Disk; d != nil {
		for _, v := range []*float64{d.LowGiB, d.CriticalGiB, d.ConversationFloorGiB} {
			if v != nil && (*v < 0 || *v > 1<<20) {
				errs = append(errs, errors.New("disk watermarks must be between 0 and 1048576 GiB"))
				break
			}
		}
		if d.LowGiB != nil && d.CriticalGiB != nil && *d.CriticalGiB > *d.LowGiB {
			errs = append(errs, fmt.Errorf("disk.critical_watermark_gib %g must not exceed disk.low_watermark_gib %g", *d.CriticalGiB, *d.LowGiB))
		}
	}
	return errors.Join(errs...)
}

// Empty reports whether s sets nothing (every setting comes from node.yaml).
func (s NodeSettings) Empty() bool {
	return s.Slots == nil && s.ConversationSlots == nil && s.Labels == nil && len(s.Drivers) == 0 && s.Disk == nil
}

// Keys are the report keys s sets: slots, conversation_slots, labels, drivers.<name>, disk.
func (s NodeSettings) Keys() []string {
	var out []string
	if s.Slots != nil {
		out = append(out, SettingSlots)
	}
	if s.ConversationSlots != nil {
		out = append(out, SettingConversationSlots)
	}
	if s.Labels != nil {
		out = append(out, SettingLabels)
	}
	for _, name := range RemoteDrivers {
		if d, ok := s.Drivers[name]; ok && d.Enabled != nil {
			out = append(out, SettingDrivers+"."+name)
		}
	}
	if s.Disk != nil && (s.Disk.LowGiB != nil || s.Disk.CriticalGiB != nil || s.Disk.ConversationFloorGiB != nil) {
		out = append(out, SettingDisk)
	}
	return out
}

// CompareVersions orders node package versions (node-packaging-updates C): the dot-separated numbers before any
// '+' or '-' suffix, missing parts 0 (2026.11.12.1530 < 2026.11.12.1531 < 2026.11.13). ok is false when either is
// not such a version (empty, "dev", a commit hash): such versions are not ordered.
func CompareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := versionParts(a)
	pb, okb := versionParts(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range max(len(pa), len(pb)) {
		var x, y int64
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
	}
	return 0, true
}

func versionParts(v string) ([]int64, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil, false
	}
	var out []int64
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// VersionBelow reports whether a node of version v is below min (node-packaging-updates C, min_node_version): never
// without a minimum; always for a node that reports no version (it predates version reporting); never for a version
// that is not ordered (a development build such as "dev").
func VersionBelow(v, min string) bool {
	if strings.TrimSpace(min) == "" {
		return false
	}
	if strings.TrimSpace(v) == "" {
		return true
	}
	c, ok := CompareVersions(v, min)
	return ok && c < 0
}
