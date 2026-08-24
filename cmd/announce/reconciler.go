package announce

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

//nolint:gocognit // TODO: refactor
//revive:disable:cyclomatic // TODO: refactor
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger

	var svcs corev1.ServiceList
	if err := r.client.List(ctx, &svcs, client.MatchingLabelsSelector{Selector: r.selector}); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed listing services: %w", err)
	}

	cfg := l4proxyconfig.Config{
		APIVersion: l4proxyconfig.APIVersionV1,
	}

	for idx := range svcs.Items {
		svc := svcs.Items[idx]
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
							log.Error(err, "failed parsing annotation value",
								"namespace", svc.Namespace,
								"service", svc.Name,
								"annotation", AnnotationHealthInterval,
							)
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
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			log.Error(closeErr, "failed to close output file")
		}
	}()

	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed marshaling config: %w", err)
	}

	log.Info("updated configuration file", "frontends", len(cfg.Frontends))

	return reconcile.Result{}, nil
}
