package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/model"
)

type Detector struct {
	backend  backend.ClientBackend
	mu       sync.Mutex
	agents   map[string]*model.AgentNode // agentID -> node
	pidToAgent map[int32]string          // PID -> agentID (for stability)
	nextID   int
}

func NewDetector(be backend.ClientBackend) *Detector {
	return &Detector{
		backend:    be,
		agents:     make(map[string]*model.AgentNode),
		pidToAgent: make(map[int32]string),
	}
}

// Update processes the latest process tree and returns the agent hierarchy.
func (d *Detector) Update(root *model.ProcessInfo) []*model.AgentNode {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Classify all processes
	d.classifyTree(root)

	// Build agent hierarchy
	agents := d.buildHierarchy(root)

	return agents
}

func (d *Detector) classifyTree(node *model.ProcessInfo) {
	node.Role = d.backend.ClassifyProcess(node)

	// Assign stable agent ID if this is an agent
	if node.Role == model.RoleOrchestrator || node.Role == model.RoleSubAgent {
		if id, ok := d.pidToAgent[node.PID]; ok {
			node.AgentID = id
		} else {
			id := fmt.Sprintf("agent-%d", d.nextID)
			d.nextID++
			d.pidToAgent[node.PID] = id
			node.AgentID = id
		}
	}

	for _, child := range node.Children {
		d.classifyTree(child)
	}
}

func (d *Detector) buildHierarchy(root *model.ProcessInfo) []*model.AgentNode {
	var result []*model.AgentNode
	newAgents := make(map[string]*model.AgentNode)

	var walk func(node *model.ProcessInfo, parentAgentID string)
	walk = func(node *model.ProcessInfo, parentAgentID string) {
		if node.Role == model.RoleOrchestrator || node.Role == model.RoleSubAgent {
			agent := d.getOrCreateAgent(node, parentAgentID)
			agent.Processes = []*model.ProcessInfo{node} // reset, re-attach below
			newAgents[agent.ID] = agent
			result = append(result, agent)

			// Children of this agent
			for _, child := range node.Children {
				walk(child, agent.ID)
			}
		} else {
			// Non-agent process: attach to parent agent
			if parentAgentID != "" {
				if parent, ok := newAgents[parentAgentID]; ok {
					parent.Processes = append(parent.Processes, node)
				}
			}
			for _, child := range node.Children {
				walk(child, parentAgentID)
			}
		}
	}

	walk(root, "")
	d.agents = newAgents
	return result
}

func (d *Detector) getOrCreateAgent(proc *model.ProcessInfo, parentID string) *model.AgentNode {
	if existing, ok := d.agents[proc.AgentID]; ok {
		existing.ParentID = parentID
		return existing
	}
	return &model.AgentNode{
		ID:        proc.AgentID,
		Role:      proc.Role,
		PID:       proc.PID,
		ParentID:  parentID,
		SpawnTime: time.Now(),
	}
}

// Agents returns the current agent map.
func (d *Detector) Agents() map[string]*model.AgentNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.agents
}
