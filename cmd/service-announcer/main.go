package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	l4proxyconfig "github.com/makkes/l4proxy/config"
)

type Reconciler struct {
	client         client.Client
	healthInterval int
	logger         logr.Logger
	l4ProxyConfig  string
	bind           string
	selector       labels.Selector
}

const AnnotationHealthInterval = "l4proxy.e13.dev/health-interval"

func (r Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var svc corev1.Service
	log := r.logger
	if err := r.client.Get(ctx, req.NamespacedName, &svc); err != nil {
		if errors.IsNotFound(err) {
			// Service is gone, just carry on
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		log.Info("skipping non-LoadBalancer service", "namespace", svc.Namespace, "name", svc.Name)
		return reconcile.Result{}, nil
	}

	var svcs corev1.ServiceList
	if err := r.client.List(ctx, &svcs, client.MatchingLabelsSelector{Selector: r.selector}); err != nil {
		return reconcile.Result{}, err
	}

	cfg := l4proxyconfig.Config{
		APIVersion: l4proxyconfig.APIVersionV1,
	}

	for _, svc := range svcs.Items {
		if svc.DeletionTimestamp != nil && !svc.DeletionTimestamp.IsZero() {
			continue
		}
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			for _, port := range svc.Spec.Ports {
				if port.Protocol == "TCP" {
					fe := l4proxyconfig.Frontend{
						Bind: fmt.Sprintf("%s:%d", r.bind, port.Port),
						Backends: []l4proxyconfig.Backend{{
							Address: fmt.Sprintf("%s:%d", ingress.IP, port.Port),
						}},
						HealthInterval: r.healthInterval,
					}
					if hiAnn, ok := svc.Annotations[AnnotationHealthInterval]; ok {
						hi, err := strconv.Atoi(hiAnn)
						if err != nil {
							log.Error(err, "failed parsing annotation value", "namespace", svc.Namespace, "service", svc.Name, "annotation", AnnotationHealthInterval)
						}
						fe.HealthInterval = hi
					}
					cfg.Frontends = append(cfg.Frontends, fe)
				}
			}
		}
	}

	out, err := os.OpenFile(r.l4ProxyConfig, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not open output file: %w", err)
	}
	defer out.Close()

	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed marshalling config: %w", err)
	}

	log.Info("updated configuration file")

	return reconcile.Result{}, nil
}

func (r *Reconciler) InjectLogger(l logr.Logger) error { //nolint:unparam // this is an interface implementation
	r.logger = l
	return nil
}

func main() {
	var (
		metricsAddr       string
		l4ProxyConfigFlag string
		bindFlag          string
		selectorFlag      string
		setupLog          = ctrl.Log.WithName("setup")
	)

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	flags := flag.NewFlagSet("main", flag.ExitOnError)
	flags.StringVar(&metricsAddr, "metrics-bind-address", envOrDefault("METRICS_ADDR", ":8080"), "The address the metric endpoint binds to.")
	flags.StringVar(&l4ProxyConfigFlag, "l4proxy-config", "", "The path of the l4proxy config file.")
	flags.StringVar(&bindFlag, "bind", "", "The address that l4proxy will bind to")
	flags.StringVar(&selectorFlag, "label-selector", "", "Label selector used to select Services to"+
		"be included in the proxy configuration. See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors "+
		"for documentation on its syntax.")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "failed parsing command-line flags: %s\n", err.Error())
		os.Exit(1)
	}

	if l4ProxyConfigFlag == "" {
		setupLog.Error(fmt.Errorf("l4proxy config file not set"), "--l4proxy-config cannot be empty")
		os.Exit(1)
	}

	selector, err := labels.Parse(selectorFlag)
	if err != nil {
		setupLog.Error(err, "failed parsing --label-selector flag")
		os.Exit(1)
	}

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Metrics: metricsserver.Options{BindAddress: metricsAddr},
	})
	if err != nil {
		setupLog.Error(err, "failed creating manager instance")
		os.Exit(1)
	}

	err = builder.ControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(&Reconciler{
			logger:         mgr.GetLogger(),
			client:         mgr.GetClient(),
			l4ProxyConfig:  l4ProxyConfigFlag,
			bind:           bindFlag,
			healthInterval: 5,
			selector:       selector,
		})
	if err != nil {
		panic(err)
	}

	if err := mgr.Start(context.TODO()); err != nil {
		panic(err)
	}
}

func envOrDefault(envName, defaultValue string) string {
	ret := os.Getenv(envName)
	if ret != "" {
		return ret
	}

	return defaultValue
}
