#!/bin/bash
# Local Development DNS Setup
# This script adds entries to /etc/hosts for local testing

set -e

INGRESS_IP=${1:-127.0.0.1}

echo "Adding Zero-Ops DNS entries to /etc/hosts..."
echo "Using Ingress IP: $INGRESS_IP"

# Check if entries already exist
if grep -q "api.nutgraf.in" /etc/hosts; then
    echo "Entries already exist in /etc/hosts"
    echo "Remove them manually or run: sudo sed -i '' '/nutgraf.in/d' /etc/hosts"
    exit 1
fi

# Add entries
sudo tee -a /etc/hosts > /dev/null <<EOF

# Zero-Ops Demo 1 - Local Development
$INGRESS_IP api.nutgraf.in
$INGRESS_IP auth.nutgraf.in
$INGRESS_IP console.nutgraf.in
EOF

echo "✅ DNS entries added to /etc/hosts"
echo ""
echo "To remove these entries later, run:"
echo "  sudo sed -i '' '/Zero-Ops Demo 1/,+3d' /etc/hosts"
