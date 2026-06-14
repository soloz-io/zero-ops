<#
.SYNOPSIS
Creates the Windows Ingestion Engine (Kind cluster) with the CAPD volume mount fix.

.DESCRIPTION
This script creates a Kind cluster on Windows natively, installs the CAPD controllers, 
and fixes the Docker-in-Docker volume locality bug by mounting the host's /tmp directory.
#>

$ErrorActionPreference = "Stop"

$ClusterName = "zero-ops-windows-engine"
$WindowsIp = (Test-Connection -ComputerName $env:COMPUTERNAME -Count 1).IPv4Address.IPAddressToString

Write-Host "💻 Creating Windows Ingestion Engine natively..." -ForegroundColor Cyan

# The CRITICAL FIX: We must mount the Docker Desktop VM's /tmp directory into the Kind container.
# This solves the CAPD Volume Locality bug completely!
$kindConfig = @"
apiVersion: kind.x-k8s.io/v1alpha4
kind: Cluster
networking:
  apiServerAddress: "0.0.0.0"
  apiServerPort: 6444
nodes:
- role: control-plane
  extraMounts:
    - hostPath: /var/run/docker.sock
      containerPath: /var/run/docker.sock
    - hostPath: /tmp
      containerPath: /tmp
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      certSANs:
      - "${WindowsIp}"
      - "localhost"
      - "127.0.0.1"
"@

$kindConfig | Out-File -FilePath "$env:TEMP\kind-windows-config.yaml" -Encoding ASCII

# Create cluster natively
kind create cluster --name $ClusterName --config "$env:TEMP\kind-windows-config.yaml"
Remove-Item "$env:TEMP\kind-windows-config.yaml"

Write-Host "🚀 Initializing bare Cluster API and CAPD controllers on Windows..." -ForegroundColor Cyan
$env:KUBECONFIG = "$(kind get kubeconfig-path --name $ClusterName)"
$env:EXP_CLUSTER_RESOURCE_SET = "true"
clusterctl init --infrastructure docker

Write-Host "✅ Windows engine is ready! Now run bootstrap-distributed.sh on your Mac!" -ForegroundColor Green
