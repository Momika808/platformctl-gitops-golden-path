package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
)

type clusterCapacity struct {
	AllocMilliCPU int64
	AllocMemBytes int64
	UsedMilliCPU  int64
	UsedMemBytes  int64
}

func (d *doctorChecker) checkClusterCapacity(spec *appspec.ServiceApp) {
	if d.external.SkipCapacityCheck {
		d.warn("Cluster capacity check is disabled (--skip-capacity-check).")
		return
	}

	needMilliCPU, needMemBytes, err := requiredResourcesForSpec(spec)
	if err != nil {
		d.fail(fmt.Sprintf("cannot compute required resources for capacity check: %v", err))
		return
	}
	if needMilliCPU == 0 && needMemBytes == 0 {
		d.warn("Capacity check skipped because required resources resolved to zero.")
		return
	}

	capacity, err := collectClusterCapacity()
	if err != nil {
		msg := fmt.Sprintf("capacity check unavailable: %v", err)
		if d.external.StrictCapacityCheck {
			d.fail(msg)
		} else {
			d.warn(msg + " (set --strict-capacity-check to fail)")
		}
		return
	}

	freeMilliCPU := capacity.AllocMilliCPU - capacity.UsedMilliCPU
	freeMemBytes := capacity.AllocMemBytes - capacity.UsedMemBytes

	if freeMilliCPU < needMilliCPU || freeMemBytes < needMemBytes {
		d.fail(fmt.Sprintf(
			"insufficient cluster free capacity for %s: need cpu=%dm mem=%s, free cpu=%dm mem=%s",
			spec.Metadata.Name,
			needMilliCPU,
			formatBytesIEC(needMemBytes),
			freeMilliCPU,
			formatBytesIEC(freeMemBytes),
		))
	} else {
		d.ok(fmt.Sprintf(
			"cluster free capacity is sufficient for %s: need cpu=%dm mem=%s, free cpu=%dm mem=%s",
			spec.Metadata.Name,
			needMilliCPU,
			formatBytesIEC(needMemBytes),
			freeMilliCPU,
			formatBytesIEC(freeMemBytes),
		))
	}

	if err := d.checkNamespaceQuota(spec, needMilliCPU, needMemBytes); err != nil {
		if d.external.StrictCapacityCheck {
			d.fail(err.Error())
		} else {
			d.warn(err.Error() + " (set --strict-capacity-check to fail)")
		}
	}
}

func (d *doctorChecker) checkNamespaceQuota(spec *appspec.ServiceApp, needMilliCPU, needMemBytes int64) error {
	type quotaList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Hard map[string]string `json:"hard"`
				Used map[string]string `json:"used"`
			} `json:"status"`
		} `json:"items"`
	}

	out, err := kubectlJSON("get", "resourcequota", "-n", spec.Spec.Namespace, "-o", "json")
	if err != nil {
		return fmt.Errorf("namespace quota check unavailable for %s: %w", spec.Spec.Namespace, err)
	}

	var list quotaList
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("parse resourcequota json: %w", err)
	}
	if len(list.Items) == 0 {
		d.ok(fmt.Sprintf("no ResourceQuota objects in namespace %s", spec.Spec.Namespace))
		return nil
	}

	for _, item := range list.Items {
		hardCPU := item.Status.Hard["requests.cpu"]
		usedCPU := item.Status.Used["requests.cpu"]
		hardMem := item.Status.Hard["requests.memory"]
		usedMem := item.Status.Used["requests.memory"]

		if hardCPU != "" && usedCPU != "" {
			hardMilliCPU, err := parseCPUToMilli(hardCPU)
			if err != nil {
				return fmt.Errorf("quota %s hard requests.cpu parse error: %w", item.Metadata.Name, err)
			}
			usedMilliCPU, err := parseCPUToMilli(usedCPU)
			if err != nil {
				return fmt.Errorf("quota %s used requests.cpu parse error: %w", item.Metadata.Name, err)
			}
			remaining := hardMilliCPU - usedMilliCPU
			if remaining < needMilliCPU {
				return fmt.Errorf("namespace quota %s lacks cpu for %s: remaining=%dm need=%dm", item.Metadata.Name, spec.Metadata.Name, remaining, needMilliCPU)
			}
		}

		if hardMem != "" && usedMem != "" {
			hardMemBytes, err := parseMemoryToBytes(hardMem)
			if err != nil {
				return fmt.Errorf("quota %s hard requests.memory parse error: %w", item.Metadata.Name, err)
			}
			usedMemBytes, err := parseMemoryToBytes(usedMem)
			if err != nil {
				return fmt.Errorf("quota %s used requests.memory parse error: %w", item.Metadata.Name, err)
			}
			remaining := hardMemBytes - usedMemBytes
			if remaining < needMemBytes {
				return fmt.Errorf("namespace quota %s lacks memory for %s: remaining=%s need=%s", item.Metadata.Name, spec.Metadata.Name, formatBytesIEC(remaining), formatBytesIEC(needMemBytes))
			}
		}
	}

	d.ok(fmt.Sprintf("namespace ResourceQuota allows requested resources for %s", spec.Metadata.Name))
	return nil
}

func requiredResourcesForSpec(spec *appspec.ServiceApp) (milliCPU int64, memBytes int64, err error) {
	values := resourceProfileToValues(spec.Spec.Resources.Profile)
	replicas := spec.Spec.ReplicaCount
	if replicas <= 0 {
		replicas = 1
	}

	perCPU, err := parseCPUToMilli(values.Requests.CPU)
	if err != nil {
		return 0, 0, fmt.Errorf("parse cpu request %q: %w", values.Requests.CPU, err)
	}
	perMem, err := parseMemoryToBytes(values.Requests.Memory)
	if err != nil {
		return 0, 0, fmt.Errorf("parse memory request %q: %w", values.Requests.Memory, err)
	}
	return perCPU * int64(replicas), perMem * int64(replicas), nil
}

func collectClusterCapacity() (clusterCapacity, error) {
	type nodeList struct {
		Items []struct {
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
				Taints        []struct {
					Key    string `json:"key"`
					Effect string `json:"effect"`
				} `json:"taints"`
			} `json:"spec"`
			Status struct {
				Allocatable map[string]string `json:"allocatable"`
			} `json:"status"`
		} `json:"items"`
	}
	type podList struct {
		Items []struct {
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
					} `json:"resources"`
				} `json:"containers"`
				InitContainers []struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
					} `json:"resources"`
				} `json:"initContainers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}

	nodesRaw, err := kubectlJSON("get", "nodes", "-o", "json")
	if err != nil {
		return clusterCapacity{}, err
	}

	var nodes nodeList
	if err := json.Unmarshal(nodesRaw, &nodes); err != nil {
		return clusterCapacity{}, fmt.Errorf("parse nodes json: %w", err)
	}

	capacity := clusterCapacity{}
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable || hasControlPlaneNoSchedule(node.Spec.Taints) {
			continue
		}
		cpu, err := parseCPUToMilli(node.Status.Allocatable["cpu"])
		if err != nil {
			return clusterCapacity{}, fmt.Errorf("parse node allocatable cpu: %w", err)
		}
		mem, err := parseMemoryToBytes(node.Status.Allocatable["memory"])
		if err != nil {
			return clusterCapacity{}, fmt.Errorf("parse node allocatable memory: %w", err)
		}
		capacity.AllocMilliCPU += cpu
		capacity.AllocMemBytes += mem
	}
	if capacity.AllocMilliCPU == 0 || capacity.AllocMemBytes == 0 {
		return clusterCapacity{}, fmt.Errorf("no schedulable worker allocatable capacity detected")
	}

	podsRaw, err := kubectlJSON("get", "pods", "-A", "-o", "json")
	if err != nil {
		return clusterCapacity{}, err
	}
	var pods podList
	if err := json.Unmarshal(podsRaw, &pods); err != nil {
		return clusterCapacity{}, fmt.Errorf("parse pods json: %w", err)
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" {
			continue
		}
		if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
			continue
		}

		appCPU := int64(0)
		appMem := int64(0)
		for _, c := range pod.Spec.Containers {
			cpu, err := parseCPUToMilli(c.Resources.Requests["cpu"])
			if err != nil {
				return clusterCapacity{}, fmt.Errorf("parse pod container cpu request: %w", err)
			}
			mem, err := parseMemoryToBytes(c.Resources.Requests["memory"])
			if err != nil {
				return clusterCapacity{}, fmt.Errorf("parse pod container memory request: %w", err)
			}
			appCPU += cpu
			appMem += mem
		}

		initMaxCPU := int64(0)
		initMaxMem := int64(0)
		for _, c := range pod.Spec.InitContainers {
			cpu, err := parseCPUToMilli(c.Resources.Requests["cpu"])
			if err != nil {
				return clusterCapacity{}, fmt.Errorf("parse init container cpu request: %w", err)
			}
			mem, err := parseMemoryToBytes(c.Resources.Requests["memory"])
			if err != nil {
				return clusterCapacity{}, fmt.Errorf("parse init container memory request: %w", err)
			}
			if cpu > initMaxCPU {
				initMaxCPU = cpu
			}
			if mem > initMaxMem {
				initMaxMem = mem
			}
		}

		capacity.UsedMilliCPU += maxInt64(appCPU, initMaxCPU)
		capacity.UsedMemBytes += maxInt64(appMem, initMaxMem)
	}

	return capacity, nil
}

func kubectlJSON(args ...string) ([]byte, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func hasControlPlaneNoSchedule(taints []struct {
	Key    string `json:"key"`
	Effect string `json:"effect"`
}) bool {
	for _, t := range taints {
		if strings.EqualFold(t.Effect, "NoSchedule") && (t.Key == "node-role.kubernetes.io/control-plane" || t.Key == "node-role.kubernetes.io/master") {
			return true
		}
	}
	return false
}

func parseCPUToMilli(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err != nil {
			return 0, err
		}
		return v, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * 1000)), nil
}

func parseMemoryToBytes(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	units := map[string]int64{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
		"Ti": 1024 * 1024 * 1024 * 1024,
		"Pi": 1024 * 1024 * 1024 * 1024 * 1024,
		"Ei": 1024 * 1024 * 1024 * 1024 * 1024 * 1024,
		"K":  1000,
		"M":  1000 * 1000,
		"G":  1000 * 1000 * 1000,
		"T":  1000 * 1000 * 1000 * 1000,
		"P":  1000 * 1000 * 1000 * 1000 * 1000,
		"E":  1000 * 1000 * 1000 * 1000 * 1000 * 1000,
	}
	for unit, mult := range units {
		if strings.HasSuffix(s, unit) {
			base := strings.TrimSpace(strings.TrimSuffix(s, unit))
			f, err := strconv.ParseFloat(base, 64)
			if err != nil {
				return 0, err
			}
			return int64(math.Round(f * float64(mult))), nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func formatBytesIEC(v int64) string {
	if v < 0 {
		return fmt.Sprintf("-%s", formatBytesIEC(-v))
	}
	const (
		ki = 1024
		mi = ki * 1024
		gi = mi * 1024
	)
	switch {
	case v >= gi:
		return fmt.Sprintf("%.2fGi", float64(v)/float64(gi))
	case v >= mi:
		return fmt.Sprintf("%.2fMi", float64(v)/float64(mi))
	case v >= ki:
		return fmt.Sprintf("%.2fKi", float64(v)/float64(ki))
	default:
		return fmt.Sprintf("%dB", v)
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
