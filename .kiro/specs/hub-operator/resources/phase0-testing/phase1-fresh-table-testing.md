export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl exec -n hub-platform-data platform-db-1 -- psql -U postgres -d infisical -c "\dt" | head -50

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl delete secret infisical-redis-credentials -n hub-platform-security --ignore-not-found

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl exec -n hub-platform-data platform-db-1 -- psql -U postgres -c "DROP DATABASE infisical;"

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl scale deployment -n hub-platform-security platform-infisical-infisical-standalone-infisical --replicas=0

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl exec -n hub-platform-data platform-db-1 -- psql -U postgres -c "DROP DATABASE infisical;"

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl exec -n hub-platform-data platform-db-1 -- psql -U postgres -c "CREATE DATABASE infisical OWNER infisical;"

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl scale deployment -n hub-platform-security platform-infisical-infisical-standalone-infisical --replicas=1

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl delete secret infisical-auth infisical-admin -n hub-platform-ops --ignore-not-found

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-security -l app.kubernetes.io/name=infisical

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-security | grep infisical

Perfect! Database cleared and Infisical is running. Now the operator can start from Phase 0 with a fresh Infisical instance. The /api/v1/admin/bootstrap endpoint will work and return the orgID.

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl patch hubenvironment hub-production -n hub-platform-ops --type=merge -p '{"metadata":{"annotations":{"force-bootstrap":"'$(date +%s)'"}}}'