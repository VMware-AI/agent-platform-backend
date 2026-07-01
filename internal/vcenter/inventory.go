package vcenter

// DataCenter is a vSphere datacenter — the top-level node of vCenter's inventory
// tree. A DataCenter contains clusters, datastores, networks, vm folders and
// storage policies; each cluster contains ESXi hosts and vSphere resource pools.
//
// This type is referenced from the ent schema (resource_pool.inventory column)
// so the storage representation and the vcenter client wire representation are
// the same Go value.
type DataCenter struct {
	Name            string         `json:"name"`            // DC0
	Path            string         `json:"path"`            // /DC0
	Clusters        []Cluster      `json:"clusters"`        // /DC0/host/*
	Datastores      []PlacementRef `json:"datastores"`      // /DC0/datastore/*
	Networks        []PlacementRef `json:"networks"`        // /DC0/network/*  (Network + DVS)
	Folders         []PlacementRef `json:"folders"`         // /DC0/vm/*  (user vm folders only — B2)
	StoragePolicies []PlacementRef `json:"storagePolicies"` // PBM profiles; nil if PBM pull failed (A1)
}

// Cluster is a vSphere ClusterComputeResource, parented under a Datacenter's
// host folder. It owns ESXi hosts and vSphere resource pools (the nested
// "Resources" root RP is always present and never deletable).
type Cluster struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`          // /DC0/host/DC0_C0
	EsxiHosts     []PlacementRef `json:"esxiHosts"`     // /DC0/host/DC0_C0/*
	ResourcePools []PlacementRef `json:"resourcePools"` // /DC0/host/DC0_C0/Resources[/sub-pool]
}

// PlacementRef is the minimum information for an OVA deployment candidate
// resource. Name is the vCenter label (for display); Path is the full
// inventory path (for vCenter find.NewFinder). Path may be empty when the
// resource is unambiguously identified by name within its parent.
//
// JSON `omitempty` keeps the storage compact for resources where name alone
// is enough (rare in production; multi-DC disambiguation is the dominant case).
type PlacementRef struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}
