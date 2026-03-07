## Ubuntu Bootstrap: CAPH Reference Implementation

We adopt CAPH's proven Ubuntu bootstrap configuration exactly as-is for reliability and compatibility.

### CAPH's Complete Approach

**System Configuration Files**:
- `/etc/sysctl.d/99-cilium.conf` - Network filtering for CNI
- `/etc/modules-load.d/crio.conf` - Kernel modules (overlay, br_netfilter)
- `/etc/sysctl.d/99-kubernetes-cri.conf` - Bridge netfilter and IP forwarding
- `/etc/sysctl.d/99-kubelet.conf` - Memory and kernel panic settings
- `/etc/kubernetes/resolv.conf` - DNS resolution (Cloudflare 1.1.1.1)
- `/etc/systemd/system/containerd.service` - containerd systemd unit

**Runtime Installation** (preKubeadmCommands):
1. Download and install runc v1.2.5
2. Download and install containerd v1.7.26
3. Configure containerd with SystemdCgroup
4. Install kubernetes components via apt (kubelet, kubeadm, kubectl)
5. Pre-pull kubeadm images

**Why This Works**:
- All system prerequisites configured before kubeadm runs
- containerd properly integrated with systemd
- Kernel modules loaded for container networking
- DNS resolution configured
- Tested and validated by CAPH community

**Bootstrap Time**: 8-12 minutes (network dependent)
