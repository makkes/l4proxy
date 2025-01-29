module github.com/makkes/l4proxy/cmd/l4proxy

replace github.com/makkes/l4proxy => ../..

go 1.23

require (
	github.com/go-logr/glogr v1.2.2
	github.com/go-logr/logr v1.4.2
	github.com/makkes/l4proxy v0.0.0
	github.com/spf13/pflag v1.0.5
)

require (
	github.com/golang/glog v1.2.4 // indirect
	github.com/kr/text v0.2.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
