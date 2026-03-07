packer {
  required_plugins {
    hcloud = {
      source  = "github.com/hetznercloud/hcloud"
      version = "~> 1"
    }
  }
}

variable "hcloud_token" {
  type      = string
  sensitive = true
}

variable "snapshot_name" {
  type    = string
  default = "flatcar-stable"
}

variable "location" {
  type    = string
  default = "fsn1"
}

locals {
  snapshot_labels = {
    os            = "flatcar"
    type          = "base"
    builder       = "packer"
    snapshot-name = var.snapshot_name
  }
}

source "hcloud" "flatcar" {
  token        = var.hcloud_token
  image        = "ubuntu-24.04"
  location     = var.location
  server_type  = "cx23"
  rescue       = "linux64"
  ssh_username = "root"
  snapshot_name = var.snapshot_name
  snapshot_labels = local.snapshot_labels
}

build {
  sources = ["source.hcloud.flatcar"]

  provisioner "shell" {
    inline = [
      "apt-get -y install gawk",
      "curl -fsSLO --retry-delay 1 --retry 60 --retry-connrefused --retry-max-time 60 --connect-timeout 20 https://raw.githubusercontent.com/flatcar/init/flatcar-master/bin/flatcar-install",
      "chmod +x flatcar-install",
      "./flatcar-install -s -o hetzner -C stable"
    ]
  }
}
