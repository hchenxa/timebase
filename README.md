# timebase

This directory contains a simple timebased controller.

```
export GOPATH
mkdir -p $GOPATH/src/github.com/hchenxa/
cd $GOPATH/src/github.com/hchenxa/; git clone https://github.com/hchenxa/timebase.git
cd timebase
make
```

To regenerate client deepcopy
```
go get k8s.io/gengo/examples/deepcopy-gen
deepcopy-gen --v 2 --logtostderr --go-header-file "vendor/github.com/kubernetes/repo-infra/verify/boilerplate/boilerplate.go.txt" --input-dirs "github.com/hchenxa/timebase/pkg/api/icp.ibm.com/v1" --bounding-dirs "github.com/hchenxa/timebase" --output-file-base zz_generated.deepcopy
```
