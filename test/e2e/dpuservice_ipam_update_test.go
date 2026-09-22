package e2e

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dpuservicev1 "github.com/nvidia/doca-platform/api/dpuservice/v1alpha1"
	provisioningv1 "github.com/nvidia/doca-platform/api/provisioning/v1alpha1"
	dpfe2e "github.com/nvidia/doca-platform/test/e2e"
	nvipamv1 "github.com/nvidia/doca-platform/third_party/api/nvipam/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift-dpf/test/utils"
)

const loopbackDPUServiceIPAMName = "loopback"

type loopbackPodInfo struct {
	PodName        string
	PodUID         types.UID
	HostedNodeName string
	DPUNodeName    string
	IP             string
}

// TC-SVC-004: Edit DPUServiceIPAM Object
//
// Verifies that editing a DPUServiceIPAM updates the network used for future
// allocations without changing addresses already assigned to existing HBN
// pods. The loopback DPUServiceIPAM is only the example object used here; the
// behavior under test is modifying the IPAM object and checking its effect.
// One DPU is sufficient: its HBN pod is checked before reprovisioning, then
// the replacement pod is checked against the updated network. Cleanup restores
// the original IPAM configuration.
var _ = Describe("TC-SVC-004: Edit DPUServiceIPAM Object",
	Label("dpuservice", "update-dpuserviceipam"), Ordered, func() {
		var (
			originalIPAMSpec     dpuservicev1.DPUServiceIPAMSpec
			originalSpecCaptured bool
			originalNetwork      string
			updatedNetwork       string
			baselinePods         map[string]loopbackPodInfo
			baselineCIDRPool     *nvipamv1.CIDRPool
			targetDPU            provisioningv1.DPU
			targetDPUDeleted     bool
		)

		BeforeAll(func() {
			skipIfClusterNotReadyForDPUReprovisioning()
		})

		AfterAll(func() {
			if !originalSpecCaptured {
				return
			}
			var podUIDBeforeRestore types.UID
			if targetDPUDeleted {
				podUIDBeforeRestore = baselinePods[targetDPU.Spec.DPUNodeName].PodUID
			}

			// Register restoration before discovery and pod deletion: either can fail,
			// but neither should leave the shared cluster on the expanded IPAM network.
			defer func() {
				current := &dpuservicev1.DPUServiceIPAM{}
				err := mgmtClient.Get(ctx, client.ObjectKey{
					Namespace: cfg.DPFNamespace,
					Name:      loopbackDPUServiceIPAMName,
				}, current)
				if apierrors.IsNotFound(err) {
					Fail("AfterAll: loopback DPUServiceIPAM was deleted")
				}
				Expect(err).NotTo(HaveOccurred(), "AfterAll: failed to get loopback DPUServiceIPAM")

				if !reflect.DeepEqual(current.Spec, originalIPAMSpec) {
					By("AfterAll: restoring the original loopback DPUServiceIPAM")
					patch := client.MergeFrom(current.DeepCopy())
					current.Spec = *originalIPAMSpec.DeepCopy()
					Expect(mgmtClient.Patch(ctx, current, patch)).To(Succeed(),
						"AfterAll: failed to restore loopback DPUServiceIPAM")
				}

				By("AfterAll: waiting for the original loopback CIDRPool configuration")
				Eventually(func(g Gomega) {
					ipam := &dpuservicev1.DPUServiceIPAM{}
					g.Expect(mgmtClient.Get(ctx, client.ObjectKey{
						Namespace: cfg.DPFNamespace,
						Name:      loopbackDPUServiceIPAMName,
					}, ipam)).To(Succeed())
					g.Expect(ipam.Spec).To(Equal(originalIPAMSpec))
					g.Expect(ipam.Status.ObservedGeneration).To(BeNumerically(">=", ipam.Generation))
					g.Expect(isReady(ipam.Status.Conditions)).To(BeTrue())

					pool := &nvipamv1.CIDRPool{}
					g.Expect(hostedClient.Get(ctx, client.ObjectKey{
						Namespace: cfg.DPFNamespace,
						Name:      loopbackDPUServiceIPAMName,
					}, pool)).To(Succeed())
					g.Expect(pool.Spec.CIDR).To(Equal(originalNetwork))
				}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if targetDPUDeleted {
					waitForClusterHealthAfterDPUReprovisioning()
					waitForLoopbackPodInNetwork(targetDPU.Spec.DPUNodeName, podUIDBeforeRestore, originalNetwork)
					return
				}
				waitForClusterHealth()
			}()

			if targetDPUDeleted {
				var currentPods map[string]loopbackPodInfo
				By("AfterAll: discovering replacement HBN pods before restoring loopback IPAM")
				Eventually(func(g Gomega) {
					var err error
					currentPods, err = discoverLoopbackPods()
					g.Expect(err).NotTo(HaveOccurred())
				}).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
					"AfterAll: failed to discover replacement HBN pods")
				if replacementPod, ok := currentPods[targetDPU.Spec.DPUNodeName]; ok {
					podUIDBeforeRestore = replacementPod.PodUID
					By(fmt.Sprintf("AfterAll: deleting replacement HBN pod %s to release its ip_lo allocation",
						replacementPod.PodName))
					deleteLoopbackPodAndWait(replacementPod)
				}
			}
		})

		It("pre-condition: should have the loopback IPAM ready", func() {
			ipam := &dpuservicev1.DPUServiceIPAM{}
			Expect(mgmtClient.Get(ctx, client.ObjectKey{
				Namespace: cfg.DPFNamespace,
				Name:      loopbackDPUServiceIPAMName,
			}, ipam)).To(Succeed(), "loopback DPUServiceIPAM must exist")
			Expect(ipam.Spec.IPV4Network).NotTo(BeNil(),
				"loopback DPUServiceIPAM must use ipv4Network")
			Expect(ipam.Spec.IPV4Network.Network).NotTo(BeEmpty(),
				"loopback DPUServiceIPAM network must be set")
			Expect(isReady(ipam.Status.Conditions)).To(BeTrue(),
				"loopback DPUServiceIPAM must be Ready before update")

			originalIPAMSpec = *ipam.Spec.DeepCopy()
			originalSpecCaptured = true
			originalNetwork = ipam.Spec.IPV4Network.Network
			updatedNetwork = expandedIPv4Network(originalNetwork)
			GinkgoWriter.Printf("Loopback IPAM network: %s -> %s\n", originalNetwork, updatedNetwork)
		})

		It("pre-condition: should have existing HBN loopback allocations", func() {
			var err error
			baselinePods, err = discoverLoopbackPods()
			Expect(err).NotTo(HaveOccurred())
			Expect(baselinePods).NotTo(BeEmpty(), "no HBN loopback pods found")

			baselineCIDRPool = &nvipamv1.CIDRPool{}
			Eventually(func(g Gomega) {
				g.Expect(hostedClient.Get(ctx, client.ObjectKey{
					Namespace: cfg.DPFNamespace,
					Name:      loopbackDPUServiceIPAMName,
				}, baselineCIDRPool)).To(Succeed())
				g.Expect(baselineCIDRPool.Spec.CIDR).To(Equal(originalNetwork))
				g.Expect(baselineCIDRPool.Status.Allocations).NotTo(BeEmpty(),
					"loopback CIDRPool must have an existing allocation")
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			for _, pod := range baselinePods {
				Expect(cidrPoolContainsIP(baselineCIDRPool, pod.IP)).To(BeTrue(),
					"CIDRPool must contain the existing IP %s", pod.IP)
				GinkgoWriter.Printf("Existing DPU node %s: HBN pod %s, ip_lo=%s, podUID=%s\n",
					pod.DPUNodeName, pod.PodName, pod.IP, pod.PodUID)
			}

			targetDPU = chooseTargetDPU(baselinePods)
		})

		It("should edit the loopback DPUServiceIPAM and preserve existing allocations", func() {
			ipam := &dpuservicev1.DPUServiceIPAM{}
			Expect(mgmtClient.Get(ctx, client.ObjectKey{
				Namespace: cfg.DPFNamespace,
				Name:      loopbackDPUServiceIPAMName,
			}, ipam)).To(Succeed())
			Expect(ipam.Spec.IPV4Network).NotTo(BeNil())
			Expect(ipam.Spec.IPV4Network.Network).To(Equal(originalNetwork))

			patch := client.MergeFrom(ipam.DeepCopy())
			ipam.Spec.IPV4Network.Network = updatedNetwork
			By(fmt.Sprintf("Patching loopback DPUServiceIPAM network %s -> %s",
				originalNetwork, updatedNetwork))
			Expect(mgmtClient.Patch(ctx, ipam, patch)).To(Succeed())

			By("Waiting for the updated loopback CIDRPool to be reconciled")
			Eventually(func(g Gomega) {
				currentIPAM := &dpuservicev1.DPUServiceIPAM{}
				g.Expect(mgmtClient.Get(ctx, client.ObjectKey{
					Namespace: cfg.DPFNamespace,
					Name:      loopbackDPUServiceIPAMName,
				}, currentIPAM)).To(Succeed())
				g.Expect(currentIPAM.Status.ObservedGeneration).To(BeNumerically(">=", currentIPAM.Generation))
				g.Expect(isReady(currentIPAM.Status.Conditions)).To(BeTrue())

				pool := &nvipamv1.CIDRPool{}
				g.Expect(hostedClient.Get(ctx, client.ObjectKey{
					Namespace: cfg.DPFNamespace,
					Name:      loopbackDPUServiceIPAMName,
				}, pool)).To(Succeed())
				g.Expect(pool.Spec.CIDR).To(Equal(updatedNetwork))
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("Verifying existing HBN pods retain their loopback addresses")
			Eventually(func(g Gomega) {
				currentPods, err := discoverLoopbackPods()
				g.Expect(err).NotTo(HaveOccurred())
				for dpuNodeName, originalPod := range baselinePods {
					currentPod, ok := currentPods[dpuNodeName]
					g.Expect(ok).To(BeTrue(), "existing DPU node %s must retain an HBN pod", dpuNodeName)
					g.Expect(currentPod.PodUID).To(Equal(originalPod.PodUID),
						"existing DPU node %s HBN pod must not be replaced", dpuNodeName)
					g.Expect(currentPod.IP).To(Equal(originalPod.IP),
						"existing DPU node %s must retain ip_lo", dpuNodeName)
				}
			}).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			currentPool := &nvipamv1.CIDRPool{}
			Expect(hostedClient.Get(ctx, client.ObjectKey{
				Namespace: cfg.DPFNamespace,
				Name:      loopbackDPUServiceIPAMName,
			}, currentPool)).To(Succeed())
			for _, pod := range baselinePods {
				Expect(cidrPoolContainsIP(currentPool, pod.IP)).To(BeTrue(),
					"updated CIDRPool must retain the existing allocation for %s", pod.IP)
			}
		})

		It("should provision a new DPU with the updated loopback IPAM", func() {
			oldTargetPod := baselinePods[targetDPU.Spec.DPUNodeName]
			By(fmt.Sprintf("Deleting DPU %s to trigger a new provisioning cycle", targetDPU.Name))
			Expect(mgmtClient.Delete(ctx, &targetDPU)).To(Succeed())
			targetDPUDeleted = true

			By("Waiting for a replacement DPU to become Ready")
			var replacement provisioningv1.DPU
			Eventually(func(g Gomega) {
				current := &provisioningv1.DPU{}
				err := mgmtClient.Get(ctx, client.ObjectKey{
					Namespace: cfg.DPFNamespace,
					Name:      targetDPU.Name,
				}, current)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(current.UID).NotTo(Equal(targetDPU.UID),
					"DPU must be recreated with a new UID")
				g.Expect(current.Status.Phase).To(Equal(provisioningv1.DPUReady),
					"replacement DPU must be Ready")
				replacement = *current.DeepCopy()
			}).WithTimeout(dpfe2e.DPUDeploymentReadyTimeout).WithPolling(30 * time.Second).Should(Succeed())

			waitForClusterHealthAfterDPUReprovisioning()

			By("Waiting for the replacement HBN pod and reading its loopback address")
			var currentPods map[string]loopbackPodInfo
			Eventually(func(g Gomega) {
				var err error
				currentPods, err = discoverLoopbackPods()
				g.Expect(err).NotTo(HaveOccurred())
				newTargetPod, ok := currentPods[replacement.Spec.DPUNodeName]
				g.Expect(ok).To(BeTrue(), "replacement DPU must have an HBN pod")
				g.Expect(newTargetPod.PodUID).NotTo(Equal(oldTargetPod.PodUID),
					"replacement DPU must have a new HBN pod")
			}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			currentPool := &nvipamv1.CIDRPool{}
			Expect(hostedClient.Get(ctx, client.ObjectKey{
				Namespace: cfg.DPFNamespace,
				Name:      loopbackDPUServiceIPAMName,
			}, currentPool)).To(Succeed())
			Expect(currentPool.Spec.CIDR).To(Equal(updatedNetwork))

			newTargetPod := currentPods[replacement.Spec.DPUNodeName]
			Expect(cidrContainsIP(updatedNetwork, newTargetPod.IP)).To(BeTrue(),
				"replacement DPU ip_lo %s must come from updated network %s",
				newTargetPod.IP, updatedNetwork)
			Expect(cidrPoolContainsIP(currentPool, newTargetPod.IP)).To(BeTrue(),
				"updated CIDRPool must contain replacement DPU ip_lo %s", newTargetPod.IP)

			for dpuNodeName, originalPod := range baselinePods {
				if dpuNodeName == targetDPU.Spec.DPUNodeName {
					continue
				}
				currentPod, ok := currentPods[dpuNodeName]
				Expect(ok).To(BeTrue(), "existing DPU node %s must retain an HBN pod", dpuNodeName)
				Expect(currentPod.PodUID).To(Equal(originalPod.PodUID),
					"existing DPU node %s HBN pod must not be replaced", dpuNodeName)
				Expect(currentPod.IP).To(Equal(originalPod.IP),
					"existing DPU node %s must retain ip_lo", dpuNodeName)
			}
		})

		It("should have a healthy cluster after the IPAM edit and DPU reprovisioning", func() {
			waitForClusterHealth()
		})
	})

func discoverLoopbackPods() (map[string]loopbackPodInfo, error) {
	pods, err := utils.GetRunningPods(ctx, hostedClient, cfg.DPFNamespace, nil)
	if err != nil {
		return nil, fmt.Errorf("listing hosted DPU service pods: %w", err)
	}

	result := make(map[string]loopbackPodInfo)
	for i := range pods {
		pod := &pods[i]
		if !strings.Contains(pod.Name, "-hbn-") || pod.DeletionTimestamp != nil {
			continue
		}

		node := &corev1.Node{}
		if err := hostedClient.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, node); err != nil {
			return nil, fmt.Errorf("getting hosted node %s for HBN pod %s: %w",
				pod.Spec.NodeName, pod.Name, err)
		}
		dpuNodeName := node.Labels[provisioningv1.DPUNodeNameLabel]
		if dpuNodeName == "" {
			return nil, fmt.Errorf("hosted node %s has no %s label",
				pod.Spec.NodeName, provisioningv1.DPUNodeNameLabel)
		}

		ip, err := getHBNLoopbackIP(pod.Name)
		if err != nil {
			return nil, err
		}
		if _, exists := result[dpuNodeName]; exists {
			return nil, fmt.Errorf("multiple HBN pods found for DPU node %s", dpuNodeName)
		}

		result[dpuNodeName] = loopbackPodInfo{
			PodName:        pod.Name,
			PodUID:         pod.UID,
			HostedNodeName: pod.Spec.NodeName,
			DPUNodeName:    dpuNodeName,
			IP:             ip,
		}
	}

	return result, nil
}

func getHBNLoopbackIP(podName string) (string, error) {
	result, err := utils.ExecInPod(ctx, hostedConfig, hostedClientset,
		cfg.DPFNamespace, podName, "doca-hbn",
		[]string{"ip", "-o", "-4", "addr", "show", "dev", "lo", "scope", "global"})
	if err != nil {
		return "", fmt.Errorf("getting ip_lo address on lo interface from HBN pod %s: %w", podName, err)
	}

	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" {
			continue
		}
		ip := strings.SplitN(fields[3], "/", 2)[0]
		if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
			return ip, nil
		}
	}

	return "", fmt.Errorf("no global IPv4 ip_lo address found on lo interface in HBN pod %s", podName)
}

func deleteLoopbackPodAndWait(pod loopbackPodInfo) {
	uid := pod.PodUID
	resource := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: cfg.DPFNamespace,
		Name:      pod.PodName,
	}}
	err := hostedClient.Delete(ctx, resource, &client.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	})
	Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(),
		"failed to delete HBN pod %s to release its ip_lo allocation: %v", pod.PodName, err)

	Eventually(func(g Gomega) {
		current := &corev1.Pod{}
		err := hostedClient.Get(ctx, client.ObjectKey{
			Namespace: cfg.DPFNamespace,
			Name:      pod.PodName,
		}, current)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"HBN pod %s must be deleted before the IPAM network is restored", pod.PodName)
	}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}

func waitForLoopbackPodInNetwork(dpuNodeName string, previousPodUID types.UID, network string) loopbackPodInfo {
	for attempt := 0; attempt < 3; attempt++ {
		var replacementPod loopbackPodInfo
		By(fmt.Sprintf("AfterAll: waiting for an HBN pod on DPU node %s with an address in %s",
			dpuNodeName, network))
		Eventually(func(g Gomega) {
			currentPods, err := discoverLoopbackPods()
			g.Expect(err).NotTo(HaveOccurred())
			var exists bool
			replacementPod, exists = currentPods[dpuNodeName]
			g.Expect(exists).To(BeTrue(), "replacement DPU must have an HBN pod")
			g.Expect(replacementPod.PodUID).NotTo(Equal(previousPodUID),
				"replacement HBN pod must have a new UID")
		}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

		if cidrContainsIP(network, replacementPod.IP) {
			return replacementPod
		}

		By(fmt.Sprintf("AfterAll: releasing stale ip_lo address %s from HBN pod %s",
			replacementPod.IP, replacementPod.PodName))
		deleteLoopbackPodAndWait(replacementPod)
		previousPodUID = replacementPod.PodUID
	}

	Fail(fmt.Sprintf("AfterAll: replacement HBN pod on DPU node %s did not receive an ip_lo address in %s",
		dpuNodeName, network))
	return loopbackPodInfo{}
}

func chooseTargetDPU(loopbackPods map[string]loopbackPodInfo) provisioningv1.DPU {
	dpuList := &provisioningv1.DPUList{}
	Expect(mgmtClient.List(ctx, dpuList, client.InNamespace(cfg.DPFNamespace))).To(Succeed())

	for _, dpu := range dpuList.Items {
		if dpu.Status.Phase != provisioningv1.DPUReady {
			continue
		}
		if _, ok := loopbackPods[dpu.Spec.DPUNodeName]; ok {
			return *dpu.DeepCopy()
		}
	}

	Fail("no Ready DPU has a matching HBN loopback pod")
	return provisioningv1.DPU{}
}

func expandedIPv4Network(network string) string {
	ip, cidr, err := net.ParseCIDR(network)
	Expect(err).NotTo(HaveOccurred(), "IPAM network must be a valid IPv4 CIDR")
	Expect(ip.To4()).NotTo(BeNil(), "IPAM network must be IPv4")
	prefixSize, bits := cidr.Mask.Size()
	Expect(bits).To(Equal(32), "IPAM network must be IPv4")
	Expect(prefixSize).To(BeNumerically(">", 8),
		"IPAM network must have room for an expanded test CIDR")

	updatedPrefixSize := prefixSize - 8
	updatedCIDR := &net.IPNet{
		IP:   ip.Mask(net.CIDRMask(updatedPrefixSize, 32)),
		Mask: net.CIDRMask(updatedPrefixSize, 32),
	}
	return updatedCIDR.String()
}

func cidrContainsIP(cidr string, ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	_, network, err := net.ParseCIDR(cidr)
	return err == nil && network.Contains(parsedIP)
}

func cidrPoolContainsIP(pool *nvipamv1.CIDRPool, ip string) bool {
	for _, allocation := range pool.Status.Allocations {
		if cidrContainsIP(allocation.Prefix, ip) {
			return true
		}
	}
	return false
}
