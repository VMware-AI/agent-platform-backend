package vcenter

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/sync/errgroup"
)

// FullInventory builds a structured snapshot of the vCenter inventory tree
// suitable for powering OVA deployment cascading dropdowns. It walks the
// vSphere hierarchy per datacenter (errgroup-parallel) and folds PBM
// storage policies onto each DC.
//
// The returned tree mirrors the GraphQL DataCenter/Cluster/PlacementRef
// schema: each DC carries its clusters (with esxiHosts + vSphere resource
// pools), datastores, networks, vm folders, and (when PBM succeeds) storage
// policies. When PBM fails for any reason, every DC node has
// storagePolicies == nil, so the frontend can distinguish "PBM not pulled"
// (null) from "pulled but empty" ([]).
//
// Standalone ESXi endpoints — those that expose a HostSystem but no
// Datacenter — produce an explicit error so the operator learns the
// resource pool is pointed at a single host rather than a vCenter. We do
// not silently wrap the host in a synthetic DC.
func (c *Client) FullInventory(ctx context.Context) ([]DataCenter, error) {
	start := time.Now()
	log.Printf("vcenter.FullInventory: start")
	finder := find.NewFinder(c.vc.Client, true)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil {
		log.Printf("vcenter.FullInventory: list dc failed err=%q elapsed=%s", err, time.Since(start))
		return nil, fmt.Errorf("vcenter: list dc: %w", err)
	}
	log.Printf("vcenter.FullInventory: datacenter list returned %d dc(s)", len(dcs))
	if len(dcs) == 0 {
		host := ""
		if u := c.vc.URL(); u != nil {
			host = u.Host
		}
		log.Printf("vcenter.FullInventory: no datacenter found host=%s elapsed=%s",
			host, time.Since(start))
		return nil, fmt.Errorf(
			"vcenter: no datacenter found at endpoint %q; this resource pool may point to a standalone ESXi host rather than a vCenter",
			host,
		)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	out := make([]DataCenter, 0, len(dcs))
	var mu sync.Mutex
	for _, dc := range dcs {
		dc := dc
		g.Go(func() error {
			dcStart := time.Now()
			log.Printf("vcenter.FullInventory: walk dc %s start", dc.InventoryPath)
			node, err := c.inventoryForDC(gctx, dc)
			if err != nil {
				log.Printf("vcenter.FullInventory: walk dc %s failed err=%q elapsed=%s",
					dc.InventoryPath, err, time.Since(dcStart))
				return fmt.Errorf("dc %s: %w", dc.InventoryPath, err)
			}
			log.Printf("vcenter.FullInventory: walk dc %s ok clusters=%d datastores=%d networks=%d folders=%d storage_policies=%v elapsed=%s",
				dc.InventoryPath,
				len(node.Clusters), len(node.Datastores), len(node.Networks), len(node.Folders),
				storagePolicySummary(node.StoragePolicies), time.Since(dcStart))
			mu.Lock()
			out = append(out, node)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		log.Printf("vcenter.FullInventory: failed err=%q elapsed=%s", err, time.Since(start))
		return out, err
	}

	// PBM independent endpoint (per-B1: per-DC attribution; per-A1: graceful
	// on failure — storagePolicies stays nil so the frontend sees null).
	pbmStart := time.Now()
	if profiles, perr := c.listStorageProfiles(ctx); perr != nil {
		log.Printf("vcenter.FullInventory: PBM list failed err=%q elapsed=%s (storagePolicies will be nil on all DCs)",
			perr, time.Since(pbmStart))
	} else if len(profiles) > 0 {
		attachStoragePolicies(out, profiles)
		log.Printf("vcenter.FullInventory: PBM ok profiles=%d attached to %d dc(s) elapsed=%s",
			len(profiles), len(out), time.Since(pbmStart))
	} else {
		log.Printf("vcenter.FullInventory: PBM returned 0 profiles elapsed=%s", time.Since(pbmStart))
	}
	log.Printf("vcenter.FullInventory: OK total_dcs=%d elapsed=%s", len(out), time.Since(start))
	return out, nil
}

// storagePolicySummary renders "nil"/"empty"/"count" so the dc summary
// log line is consistent with the entity layout (nil = PBM never pulled;
// empty = pulled but the vCenter has no profiles).
func storagePolicySummary(p []PlacementRef) any {
	if p == nil {
		return "nil"
	}
	if len(p) == 0 {
		return "empty"
	}
	return len(p)
}

// inventoryForDC walks one datacenter's subtree. Layout per vSphere reality:
//
//	DC > {vm folder, host folder, datastore folder, network folder}
//	  vm folder        → Folder → user folders (recursively)
//	  host folder      → ClusterComputeResource → {HostSystem, ResourcePool}
//	  datastore folder → Datastore / StoragePod
//	  network folder   → Network / DistributedVirtualSwitch (+ DVPortgroup
//	                    parented under DVS — folded under "networks")
//
// To keep the walk to a single PropertyCollector pass per DC, we issue one
// Retrieve per kind for narrow properties (name + parent) and stitch the
// tree in-memory. Path lookups are best-effort via find.InventoryPath so
// the OVA deploy form can use the full inventory path as a vCenter find
// target.
func (c *Client) inventoryForDC(ctx context.Context, dc *object.Datacenter) (DataCenter, error) {
	rootRef := dc.Reference()
	manager := view.NewManager(c.vc.Client)
	container, err := manager.CreateContainerView(ctx, rootRef, []string{
		"Folder",
		"ClusterComputeResource", "ComputeResource",
		"HostSystem", "ResourcePool",
		"Datastore", "StoragePod",
		"Network", "DistributedVirtualSwitch", "DistributedVirtualPortgroup", "OpaqueNetwork",
	}, true)
	if err != nil {
		return DataCenter{}, fmt.Errorf("vcenter: container view: %w", err)
	}
	defer func() { _ = container.Destroy(ctx) }()

	// Helper that tolerates "not found" responses from PropertyCollector when
	// the kind simply doesn't exist on this DC (e.g. a brand-new vCenter
	// without any opaque networks yet).
	each := func(kind string, ps []string, dst interface{}) error {
		err := container.Retrieve(ctx, []string{kind}, ps, dst)
		if err == nil {
			return nil
		}
		if isRetrieveNotFound(err) {
			return nil
		}
		return fmt.Errorf("retrieve %s: %w", kind, err)
	}

	var (
		clusters    []mo.ClusterComputeResource
		standalone  []mo.ComputeResource
		hosts       []mo.HostSystem
		rps         []mo.ResourcePool
		datastores  []mo.Datastore
		storagePods []mo.StoragePod
		networks    []mo.Network
		dvss        []mo.DistributedVirtualSwitch
		dvpgs       []mo.DistributedVirtualPortgroup
		opaques     []mo.OpaqueNetwork
		folders     []mo.Folder
	)
	if err := each("ClusterComputeResource", []string{"name", "parent"}, &clusters); err != nil {
		return DataCenter{}, err
	}
	// We grab ComputeResource via the same Retrieve; PropertyCollector fills
	// dst with one slice per requested kind. Because dst is a single slice
	// here, we use a follow-up call to avoid clobbering the clusters slice.
	if err := each("ComputeResource", []string{"name", "parent"}, &standalone); err != nil {
		return DataCenter{}, err
	}
	if err := each("HostSystem", []string{"name", "parent"}, &hosts); err != nil {
		return DataCenter{}, err
	}
	if err := each("ResourcePool", []string{"name", "parent"}, &rps); err != nil {
		return DataCenter{}, err
	}
	if err := each("Datastore", []string{"name", "parent"}, &datastores); err != nil {
		return DataCenter{}, err
	}
	if err := each("StoragePod", []string{"name", "parent"}, &storagePods); err != nil {
		return DataCenter{}, err
	}
	if err := each("Network", []string{"name", "parent"}, &networks); err != nil {
		return DataCenter{}, err
	}
	if err := each("DistributedVirtualSwitch", []string{"name", "parent"}, &dvss); err != nil {
		return DataCenter{}, err
	}
	if err := each("DistributedVirtualPortgroup",
		[]string{"name", "parent", "config.distributedVirtualSwitch"}, &dvpgs); err != nil {
		return DataCenter{}, err
	}
	if err := each("OpaqueNetwork", []string{"name", "parent"}, &opaques); err != nil {
		return DataCenter{}, err
	}
	if err := each("Folder", []string{"name", "parent"}, &folders); err != nil {
		return DataCenter{}, err
	}

	dcFolders, err := dc.Folders(ctx)
	if err != nil {
		return DataCenter{}, fmt.Errorf("vcenter: dc folders: %w", err)
	}

	node := DataCenter{
		Name: dc.Name(),
		Path: dc.InventoryPath,
	}

	// Clusters with their hosts + resource pools.
	for i := range clusters {
		cl := &clusters[i]
		entry := Cluster{
			Name: cl.Name,
			Path: inventoryPathOf(c.vc.Client, cl.Reference()),
		}
		for j := range hosts {
			if hosts[j].Parent != nil && hosts[j].Parent.Value == cl.Reference().Value {
				entry.EsxiHosts = append(entry.EsxiHosts, PlacementRef{
					Name: hosts[j].Name,
					Path: inventoryPathOf(c.vc.Client, hosts[j].Reference()),
				})
			}
		}
		// Pools parented directly to this cluster (top-level "Resources"
		// plus standalone host RPs). Nested RPs come up too; the deploy
		// form picks the leaf by full path so the extra entries are
		// intentional and harmless.
		for j := range rps {
			if rps[j].Parent != nil && rps[j].Parent.Value == cl.Reference().Value {
				entry.ResourcePools = append(entry.ResourcePools, PlacementRef{
					Name: rps[j].Name,
					Path: inventoryPathOf(c.vc.Client, rps[j].Reference()),
				})
			}
		}
		node.Clusters = append(node.Clusters, entry)
	}

	// Standalone (non-cluster) ComputeResources surface their hosts under a
	// synthetic "Standalone" cluster so the deploy form dropdown doesn't
	// need a separate codepath.
	if len(standalone) > 0 {
		entry := Cluster{Name: "Standalone", Path: dcFolders.HostFolder.InventoryPath}
		for i := range standalone {
			cr := &standalone[i]
			for j := range hosts {
				if hosts[j].Parent != nil && hosts[j].Parent.Value == cr.Reference().Value {
					entry.EsxiHosts = append(entry.EsxiHosts, PlacementRef{
						Name: hosts[j].Name,
						Path: inventoryPathOf(c.vc.Client, hosts[j].Reference()),
					})
				}
			}
		}
		if len(entry.EsxiHosts) > 0 {
			node.Clusters = append(node.Clusters, entry)
		}
	}

	// Datastores (a DC may host multiple; StoragePods are surfaced alongside
	// since they are "datastore clusters" in vCenter).
	for i := range datastores {
		node.Datastores = append(node.Datastores, PlacementRef{
			Name: datastores[i].Name,
			Path: inventoryPathOf(c.vc.Client, datastores[i].Reference()),
		})
	}
	for i := range storagePods {
		node.Datastores = append(node.Datastores, PlacementRef{
			Name: storagePods[i].Name,
			Path: inventoryPathOf(c.vc.Client, storagePods[i].Reference()),
		})
	}

	// Networks: standard networks, DVS, OpaqueNetwork (NSX). DVPGs are
	// nested under their DVS — we surface them flattened here as
	// "DVSName/DVPGName" so the deploy form dropdown sees one list.
	for i := range networks {
		node.Networks = append(node.Networks, PlacementRef{
			Name: networks[i].Name,
			Path: inventoryPathOf(c.vc.Client, networks[i].Reference()),
		})
	}
	for i := range dvss {
		node.Networks = append(node.Networks, PlacementRef{
			Name: dvss[i].Name,
			Path: inventoryPathOf(c.vc.Client, dvss[i].Reference()),
		})
		for j := range dvpgs {
			if dvpgs[j].Config.DistributedVirtualSwitch != nil &&
				dvpgs[j].Config.DistributedVirtualSwitch.Value == dvss[i].Reference().Value {
				node.Networks = append(node.Networks, PlacementRef{
					Name: dvss[i].Name + "/" + dvpgs[j].Name,
					Path: inventoryPathOf(c.vc.Client, dvpgs[j].Reference()),
				})
			}
		}
	}
	for i := range opaques {
		node.Networks = append(node.Networks, PlacementRef{
			Name: opaques[i].Name,
			Path: inventoryPathOf(c.vc.Client, opaques[i].Reference()),
		})
	}

	// VM folders (B2): every folder under the DC's vmFolder, recursing one
	// level deep. We exclude the four fixed sub-folders (vm/host/datastore/
	// network) and surface only user-created folders the deploy form might
	// pick as a destination.
	vmFolderRefs := map[string]struct{}{
		dcFolders.VmFolder.Reference().Value:        {},
		dcFolders.HostFolder.Reference().Value:      {},
		dcFolders.DatastoreFolder.Reference().Value: {},
		dcFolders.NetworkFolder.Reference().Value:   {},
	}
	for i := range folders {
		f := &folders[i]
		if isFixedSubFolder(f.Reference().Value, vmFolderRefs) {
			continue
		}
		// Only surface folders that live under vmFolder (one-hop parent
		// check; deeper user folders are rare in practice but accepted
		// when the operator picks them by exact path).
		if f.Parent != nil && f.Parent.Value == dcFolders.VmFolder.Reference().Value {
			node.Folders = append(node.Folders, PlacementRef{
				Name: f.Name,
				Path: inventoryPathOf(c.vc.Client, f.Reference()),
			})
		}
	}

	return node, nil
}

// isRetrieveNotFound identifies PropertyCollector "not found" errors so a
// missing entity type (e.g. no ComputeResource on a cluster-only fleet)
// doesn't fail the whole DC walk. govmomi returns SOAP faults as raw XML
// with no Go error wrapper, so we just match the canonical strings the
// SDK uses for missing objects.
func isRetrieveNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist")
}

// isFixedSubFolder is the cheap filter that excludes the four vSphere
// built-in sub-folders (vm/host/datastore/network) from the vmFolder list
// — those are organizational, never VM destinations.
func isFixedSubFolder(ref string, fixed map[string]struct{}) bool {
	_, ok := fixed[ref]
	return ok
}

// inventoryPathOf returns the human-readable inventory path for a managed
// object. On error it falls back to the ref value, so callers still get a
// non-empty (if ugly) path. The c.vc.Client field is the underlying
// *vim25.Client, which is what find.InventoryPath needs.
func inventoryPathOf(client *vim25.Client, ref types.ManagedObjectReference) string {
	if client == nil {
		return ref.Value
	}
	if p, err := find.InventoryPath(context.Background(), client, ref); err == nil && p != "" {
		return p
	}
	return ref.Value
}

// attachStoragePolicies puts the same profile list on every DC. vSphere
// has profile-to-datastore associations, but exposing them per-DS would
// force the deploy form to query for them; per-DC keeps the model simple
// and the product PR (B1 follow-up) can refine attribution.
func attachStoragePolicies(nodes []DataCenter, profiles []PlacementRef) {
	if len(profiles) == 0 {
		return
	}
	for i := range nodes {
		nodes[i].StoragePolicies = profiles
	}
}
