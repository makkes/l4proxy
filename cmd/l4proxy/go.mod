module github.com/makkes/l4proxy/cmd/l4proxy

require (
	github.com/go-logr/glogr v1.2.2
	github.com/go-logr/logr v1.3.0
	github.com/makkes/l4proxy v0.0.0
	github.com/spf13/pflag v1.0.5
)

require (
	github.com/golang/glog v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/makkes/l4proxy => ../..

go 1.19
