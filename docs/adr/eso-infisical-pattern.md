# Design patterns

## Architecture

[ Hub Init ]
   ↓
Generate credentials
   ↓
Store in Infisical  ← SOURCE OF TRUTH
   ↓
----------------------------------------
   ↓
[ ESO ]
   ↓
Sync to K8s Secrets (Hub + Spokes)
   ↓
[ Jobs / Apps ]
   ↓
Consume secrets
   ↓
[ PostgreSQL ]

## Password creation lifecycle

[ Bootstrap Job ]
     ↓
Generates DB credentials
     ↓
Stores in Infisical
     ↓
-----------------------------------
     ↓
[ Spoke Cluster ]
     ↓
ESO pulls from Infisical
     ↓
Creates K8s Secret
     ↓
App consumes

## Boundry line

- Use Infisical for secrets only, not for service discovery or configuration