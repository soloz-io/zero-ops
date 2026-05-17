

# SSH access Command

## Ed25519 key (recommended):
ssh-keygen -t ed25519 -C "arun4infra@gmail.com"
cat ~/.ssh/id_ed25519.pub

## If you prefer RSA:
ssh-keygen -t rsa -b 4096 -C "arun4infra@gmail.com"