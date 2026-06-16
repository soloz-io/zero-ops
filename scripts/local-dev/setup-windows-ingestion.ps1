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
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      certSANs:
      - "${WindowsIp}"
      - "localhost"
      - "127.0.0.1"
      - "0.0.0.0"
"@

$kindConfig | Out-File -FilePath "$env:TEMP\kind-windows-config.yaml" -Encoding ASCII

$binDir = Join-Path $PWD "bin"
if (-not (Test-Path $binDir)) { New-Item -Path $binDir -ItemType Directory | Out-Null }
$env:PATH = "$binDir;$env:PATH"

if (-not (Get-Command kind -ErrorAction SilentlyContinue)) {
    Write-Host "Downloading kind.exe..." -ForegroundColor Yellow
    Invoke-WebRequest -Uri "https://kind.sigs.k8s.io/dl/v0.22.0/kind-windows-amd64" -OutFile "$binDir\kind.exe"
}
if (-not (Get-Command clusterctl -ErrorAction SilentlyContinue)) {
    Write-Host "Downloading clusterctl.exe..." -ForegroundColor Yellow
    Invoke-WebRequest -Uri "https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.6.3/clusterctl-windows-amd64.exe" -OutFile "$binDir\clusterctl.exe"
}

# Force Windows to use its own local Docker daemon (ignoring any remote contexts)
$env:DOCKER_CONTEXT = "default"

# Ensure clean state
try { kind delete cluster --name $ClusterName 2>$null } catch { }

# Create cluster natively
kind create cluster --name $ClusterName --config "$env:TEMP\kind-windows-config.yaml"
Remove-Item "$env:TEMP\kind-windows-config.yaml"

Write-Host "🔧 Compiling and Patching custom CAPD Manager natively..." -ForegroundColor Cyan
$capiDir = "$env:TEMP\cluster-api"
if (Test-Path $capiDir) { Remove-Item -Recurse -Force $capiDir }
git clone -b v1.10.10 --depth 1 https://github.com/kubernetes-sigs/cluster-api.git $capiDir

$patchSource = Join-Path $PWD "scripts\local-dev\capd-port-mapping.patch"
Copy-Item $patchSource "$capiDir\capd-port-mapping.patch"

Push-Location $capiDir
git apply capd-port-mapping.patch
$env:DOCKER_BUILDKIT="1"
docker build --build-arg builder_image=golang:1.22.12 --build-arg goproxy=https://proxy.golang.org,direct --build-arg ARCH=amd64 . -t local/capd-manager:v1.10.10-patched -f test/infrastructure/docker/Dockerfile
Pop-Location

Write-Host "📦 Loading patched CAPD manager into kind cluster..." -ForegroundColor Cyan
kind load docker-image local/capd-manager:v1.10.10-patched --name $ClusterName

Write-Host "🚀 Initializing bare Cluster API and CAPD controllers on Windows..." -ForegroundColor Cyan

# Save the kubeconfig explicitly
kind get kubeconfig --name $ClusterName > "$env:TEMP\windows-engine-kubeconfig.yaml"
$env:KUBECONFIG = "$env:TEMP\windows-engine-kubeconfig.yaml"

$env:EXP_CLUSTER_RESOURCE_SET = "true"
$env:CLUSTER_TOPOLOGY = "true"
clusterctl init --infrastructure docker

Write-Host "🔄 Patching CAPD Deployment to use custom local image..." -ForegroundColor Cyan
# Wait for the deployment to be created by clusterctl
while (-not (kubectl get deployment capd-controller-manager -n capd-system 2>$null)) {
    Start-Sleep -Seconds 2
}
kubectl set image deployment/capd-controller-manager manager=local/capd-manager:v1.10.10-patched -n capd-system
kubectl rollout status deployment/capd-controller-manager -n capd-system --timeout=120s

Write-Host "✅ Windows engine is ready! Now run bootstrap-distributed.sh on your Mac!" -ForegroundColor Green
