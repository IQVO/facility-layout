// Package travel is the pure-domain travel graph and shortest-path
// computation (ADR-0017). It imports no repository or infrastructure
// package: it is handed a plain description of a zone's aisles and
// cross-aisles and computes distances over that description alone. This
// context owns the MAP TOPOLOGY only — travel TIME, congestion, and route
// choice under load are wes-work-planning's concern, not this package's.
package travel

import (
	"container/heap"
	"errors"
	"math"
)

var (
	// ErrUnknownNode is returned when a distance query names a node
	// (aisle+bay) the graph has no waypoint for.
	ErrUnknownNode = errors.New("travel graph has no waypoint for this aisle/bay")
	// ErrNoRoute is returned when no path exists between two nodes that
	// both exist in the graph — e.g. a one-way aisle makes one direction
	// unreachable, or the two nodes are in disconnected components.
	ErrNoRoute = errors.New("no route exists between these two waypoints")
)

// Node is one waypoint on the travel graph: a bay ordinal on a specific
// aisle. AisleID is the aisle's full identity (SITE-AREA-ZONE-AISLE); Bay
// is the bay segment of a LocationCode, e.g. "03".
type Node struct {
	AisleID string
	Bay     string
}

// Edge is one directed, weighted connection between two Nodes: either two
// adjacent bays on the same aisle, or a cross-aisle link between two
// aisles at a shared bay. Estimated is true when MetresM came from a
// zone's bay/level pitch rather than real geometry.
type Edge struct {
	From      Node
	To        Node
	MetresM   float64
	Estimated bool
}

// AisleGeom describes one aisle's shape for graph construction: its full
// identity, its ordered list of bay codes (in physical walk order along
// the aisle), its traffic Direction, and its optional centreline length —
// when CentrelineM is 0 the graph falls back to BayPitchM for adjacent-bay
// distances and flags them Estimated.
type AisleGeom struct {
	AisleID     string
	Bays        []string
	OneWay      bool
	CentrelineM float64 // 0 means "no centreline geometry"
}

// CrossAisleRef describes one active cross-aisle connection for graph
// construction: the two aisles it links and the bay it connects them at.
// The graph does not need to know the connecting walkway's own geometry —
// it estimates the connection's length from the two aisles' pitches
// (ADR-0017 leaves cross-aisle-specific geometry to a later phase).
type CrossAisleRef struct {
	FromAisleID string
	ToAisleID   string
	AtBay       string
}

// Pitch supplies the fallback per-bay and per-level distance for a zone
// when an aisle carries no centreline geometry (ADR-0017). LevelPitchM is
// the estimated vertical spacing between adjacent levels; the bay graph
// itself has no level dimension (nodes are aisle+bay waypoints), so it is
// consumed by the distance use case for the vertical component of a route
// whose endpoints sit on different levels without real z geometry.
type Pitch struct {
	BayPitchM   float64
	LevelPitchM float64
}

// Graph is an immutable adjacency-list representation of one zone's
// travel topology, ready for repeated Distance queries.
type Graph struct {
	adjacency map[Node][]Edge
	nodes     map[Node]struct{}
	pitch     Pitch
}

// LevelPitchM returns the zone's estimated vertical spacing between
// adjacent levels, carried through from Build for callers that must price
// a level change no bay-graph edge can see.
func (g Graph) LevelPitchM() float64 { return g.pitch.LevelPitchM }

// Route is the result of a successful Distance query: the total length in
// metres, whether any segment of it was estimated rather than measured,
// and the ordered list of waypoints traversed (including both endpoints).
type Route struct {
	MetresM   float64
	Estimated bool
	Nodes     []Node
}

// Build constructs a Graph from a zone's aisles and cross-aisles. Each
// aisle contributes one edge per pair of adjacent bays (both directions
// when the aisle is TwoWay, one direction only — walk-order forward —
// when OneWay); each cross-aisle contributes one bidirectional edge pair
// between its two aisles at their shared bay.
func Build(aisles []AisleGeom, crossAisles []CrossAisleRef, pitch Pitch) Graph {
	g := Graph{adjacency: make(map[Node][]Edge), nodes: make(map[Node]struct{}), pitch: pitch}

	for _, a := range aisles {
		for i, bay := range a.Bays {
			g.nodes[Node{AisleID: a.AisleID, Bay: bay}] = struct{}{}
			if i == 0 {
				continue
			}
			from := Node{AisleID: a.AisleID, Bay: a.Bays[i-1]}
			to := Node{AisleID: a.AisleID, Bay: bay}
			metres, estimated := bayDistance(a, pitch)
			g.addEdge(from, to, metres, estimated)
			if !a.OneWay {
				g.addEdge(to, from, metres, estimated)
			}
		}
	}

	for _, c := range crossAisles {
		from := Node{AisleID: c.FromAisleID, Bay: c.AtBay}
		to := Node{AisleID: c.ToAisleID, Bay: c.AtBay}
		if _, ok := g.nodes[from]; !ok {
			continue
		}
		if _, ok := g.nodes[to]; !ok {
			continue
		}
		metres, estimated := crossAisleDistance(pitch)
		g.addEdge(from, to, metres, estimated)
		g.addEdge(to, from, metres, estimated)
	}

	return g
}

func (g *Graph) addEdge(from, to Node, metres float64, estimated bool) {
	g.adjacency[from] = append(g.adjacency[from], Edge{From: from, To: to, MetresM: metres, Estimated: estimated})
}

// bayDistance returns the per-adjacent-bay distance for an aisle: the
// centreline length divided evenly across its bay gaps when geometry is
// known, else the zone's bay pitch (flagged Estimated).
func bayDistance(a AisleGeom, pitch Pitch) (metres float64, estimated bool) {
	gaps := len(a.Bays) - 1
	if a.CentrelineM > 0 && gaps > 0 {
		return a.CentrelineM / float64(gaps), false
	}
	return pitch.BayPitchM, true
}

// crossAisleDistance returns the estimated length of a cross-aisle
// connection. This phase estimates every cross-aisle from the zone's bay
// pitch — cross-aisle-specific geometry (e.g. its own centreline) is left
// to a later phase, so every cross-aisle edge is Estimated=true today.
func crossAisleDistance(pitch Pitch) (metres float64, estimated bool) {
	return pitch.BayPitchM, true
}

// AllNodes returns every waypoint in the graph, in no particular order.
func (g Graph) AllNodes() []Node {
	nodes := make([]Node, 0, len(g.nodes))
	for n := range g.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// AllEdges returns every directed edge in the graph, in no particular
// order.
func (g Graph) AllEdges() []Edge {
	edges := make([]Edge, 0, len(g.adjacency))
	for _, es := range g.adjacency {
		edges = append(edges, es...)
	}
	return edges
}

// Distance computes the shortest path between from and to via Dijkstra's
// algorithm, honouring edge direction (so a OneWay aisle's reverse
// direction is simply absent from the graph). Returns ErrUnknownNode if
// either endpoint is not in the graph, ErrNoRoute if the graph has both
// nodes but no directed path connects them.
func (g Graph) Distance(from, to Node) (Route, error) {
	if _, ok := g.nodes[from]; !ok {
		return Route{}, ErrUnknownNode
	}
	if _, ok := g.nodes[to]; !ok {
		return Route{}, ErrUnknownNode
	}
	if from == to {
		return Route{MetresM: 0, Estimated: false, Nodes: []Node{from}}, nil
	}

	dist := make(map[Node]float64, len(g.nodes))
	estimated := make(map[Node]bool, len(g.nodes))
	prev := make(map[Node]Node, len(g.nodes))
	visited := make(map[Node]bool, len(g.nodes))
	for n := range g.nodes {
		dist[n] = math.Inf(1)
	}
	dist[from] = 0

	pq := &priorityQueue{{node: from, dist: 0}}
	heap.Init(pq)

	for pq.Len() > 0 {
		current := heap.Pop(pq).(pqItem)
		if visited[current.node] {
			continue
		}
		visited[current.node] = true
		if current.node == to {
			break
		}
		for _, edge := range g.adjacency[current.node] {
			candidate := dist[current.node] + edge.MetresM
			if candidate < dist[edge.To] {
				dist[edge.To] = candidate
				estimated[edge.To] = estimated[current.node] || edge.Estimated
				prev[edge.To] = current.node
				heap.Push(pq, pqItem{node: edge.To, dist: candidate})
			}
		}
	}

	if math.IsInf(dist[to], 1) {
		return Route{}, ErrNoRoute
	}

	nodes := []Node{to}
	for n := to; n != from; {
		p, ok := prev[n]
		if !ok {
			return Route{}, ErrNoRoute
		}
		nodes = append(nodes, p)
		n = p
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}

	return Route{MetresM: dist[to], Estimated: estimated[to], Nodes: nodes}, nil
}

// pqItem is one entry in the Dijkstra priority queue.
type pqItem struct {
	node Node
	dist float64
}

// priorityQueue is a container/heap min-heap over pqItem.dist.
type priorityQueue []pqItem

func (pq priorityQueue) Len() int            { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool  { return pq[i].dist < pq[j].dist }
func (pq priorityQueue) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *priorityQueue) Push(x interface{}) { *pq = append(*pq, x.(pqItem)) }
func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[:n-1]
	return item
}
