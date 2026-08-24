// Package announce write an L4proxy configuration from Kubernetes LoadBalancer services.
package announce

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func NewCommand(log **slog.Logger) *cobra.Command {
	var (
		metricsAddr       string
		l4ProxyConfigFlag string
		bindFlag          string
		selectorFlag      string
	)

	cmd := &cobra.Command{
		Use:   "announce",
		Short: "run the service announcer",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if l4ProxyConfigFlag == "" {
				return errors.New("--l4proxy-config cannot be empty")
			}
			selector, err := labels.Parse(selectorFlag)
			if err != nil {
				return fmt.Errorf("failed parsing --label-selector flag: %w", err)
			}

			return run(cmd.Context(), *log, selector, metricsAddr, l4ProxyConfigFlag, bindFlag)
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", envOrDefault("METRICS_ADDR", ":8080"), "The address the metric endpoint binds to.")
	cmd.Flags().StringVar(&l4ProxyConfigFlag, "l4proxy-config", "", "The path of the l4proxy config file.")
	cmd.Flags().StringVar(&bindFlag, "bind", "", "The address that l4proxy will bind to")
	cmd.Flags().StringVar(&selectorFlag, "label-selector", "", "Label selector used to select Services to"+
		"be included in the proxy configuration. See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors "+
		"for documentation on its syntax.")

	return cmd
}

func run(ctx context.Context, log *slog.Logger, selector labels.Selector, metricsAddr, l4ProxyConfig, bindFlag string) error {
	ctrl.SetLogger(logr.FromSlogHandler(log.Handler()))

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Metrics: metricsserver.Options{BindAddress: metricsAddr},
	})
	if err != nil {
		return fmt.Errorf("failed creating manager instance: %w", err)
	}

	err = builder.ControllerManagedBy(mgr).
		Named("all-services").
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name: "all-services",
						},
					},
				}
			},
		)).
		Complete(&Reconciler{
			logger:         mgr.GetLogger(),
			client:         mgr.GetClient(),
			l4ProxyConfig:  l4ProxyConfig,
			bind:           bindFlag,
			healthInterval: 5,
			selector:       selector,
		})
	if err != nil {
		panic(err)
	}

	return mgr.Start(ctx)
}

func envOrDefault(envName, defaultValue string) string {
	ret := os.Getenv(envName)
	if ret != "" {
		return ret
	}

	return defaultValue
}
