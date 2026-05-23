# E2E Tests

End-to-end tests using [kubernetes-sigs/e2e-framework](https://github.com/kubernetes-sigs/e2e-framework).

## Structure

```
test/e2e/
├── go.mod              # Separate test module
├── kube-sbt-api/       # OpenMeter provider tests
│   ├── openmeter_test.go
│   └── README.md
└── README.md
```

## Running Tests

### Local Development

```bash
# 1. Start port-forward (in separate terminal)
kubectl port-forward -n platform-billing svc/openmeter-api 8080:80

# 2. Run tests
export KUBECONFIG=../k8-secrets/kubeconfig/hub.kubeconfig
export OPENMETER_URL=http://localhost:8080
cd test/e2e
go test -v ./kube-sbt-api
```

### CI/CD

```bash
export KUBECONFIG=/path/to/kubeconfig
cd test/e2e
go test -v ./...
```

## Adding New Tests

1. Create test package under `test/e2e/`
2. Use e2e-framework patterns (TestMain, features, assessments)
3. Update this README

## Framework

Tests use standard `go test` with e2e-framework for:
- Kubeconfig resolution (`conf.ResolveKubeConfigFile()`)
- Feature-based organization
- Context-aware execution
- Setup/Teardown hooks
