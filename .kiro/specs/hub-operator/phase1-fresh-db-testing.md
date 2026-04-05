export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl delete deployment platform-infisical-infisical-standalone-infisical -n hub-platform-security

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get statefulset -n hub-platform-data | grep redis

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl delete statefulset redis-master -n hub-platform-data

sleep 10 && export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-security | grep infisical

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-data | grep redis


sleep 15 && export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-security | grep infisical

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl delete secret infisical-auth infisical-admin -n hub-platform-ops --ignore-not-found

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl apply -k operators/hub-operator/config/default

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get pods -n hub-platform-ops

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl patch hubenvironment hub-production -n hub-platform-ops --type=merge -p '{"metadata":{"annotations":{"force-bootstrap":"'$(date +%s)'"}}}'

sleep 5 && export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get secret infisical-auth infisical-admin -n hub-platform-ops

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl get secret infisical-admin -n hub-platform-ops -o jsonpath='{.data.project-slug}' | base64 -d

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl logs -n hub-platform-ops controller-manager-69f48bfb6d-kcl7n | grep -i "infisical\|bootstrap\|phase" | tail -50

export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig && kubectl logs -n hub-platform-ops controller-manager-69f48bfb6d-ckxqm --tail=50
