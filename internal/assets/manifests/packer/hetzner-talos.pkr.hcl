packer {
  required_plugins {
    hcloud = {
      source  = "github.com/hetznercloud/hcloud"
      version = "~> 1"
    }
  }
}

variable "talos_version" {
  type    = string
  default = "v1.12.0"
}

variable "arch" {
  type    = string
  default = "amd64"
}

variable "server_type" {
  type    = string
  default = "cx23"
}

variable "server_location" {
  type    = string
  default = "nbg1"
}

variable "snapshot_name" {
  type    = string
  default = "talos-v1.12.0-amd64"
}

locals {
  image = "https://factory.talos.dev/image/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba/${var.talos_version}/hcloud-${var.arch}.raw.xz"
}

source "hcloud" "talos" {
  rescue       = "linux64"
  image        = "ubuntu-22.04"
  location     = var.server_location
  server_type  = var.server_type
  ssh_username = "root"

  snapshot_name   = var.snapshot_name
  snapshot_labels = {
    type    = "talos"
    version = var.talos_version
    arch    = var.arch
  }
}

build {
  sources = ["source.hcloud.talos"]

  provisioner "shell" {
    inline = [
      "apt-get update && apt-get install -y wget xz-utils",
      "wget -O /tmp/talos.raw.xz ${local.image}",
      "xz -d -c /tmp/talos.raw.xz | dd of=/dev/sda && sync",
    ]
  }
}
