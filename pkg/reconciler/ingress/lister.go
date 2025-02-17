/*
Copyright 2021 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ingress

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"knative.dev/networking/pkg/apis/networking/v1alpha1"
	"knative.dev/networking/pkg/status"

	"knative.dev/net-gateway-api/pkg/reconciler/ingress/config"
)

func NewProbeTargetLister(logger *zap.SugaredLogger, endpointsLister corev1listers.EndpointsLister) status.ProbeTargetLister {
	return &gatewayPodTargetLister{
		logger:          logger,
		endpointsLister: endpointsLister,
	}
}

type gatewayPodTargetLister struct {
	logger          *zap.SugaredLogger
	endpointsLister corev1listers.EndpointsLister
}

func (l *gatewayPodTargetLister) ListProbeTargets(ctx context.Context, ing *v1alpha1.Ingress) ([]status.ProbeTarget, error) {
	result := make([]status.ProbeTarget, 0, len(ing.Spec.Rules))
	for _, rule := range ing.Spec.Rules {
		eps, err := l.getRuleProbes(ctx, rule, ing.Spec.HTTPOption)
		if err != nil {
			return nil, err
		}
		result = append(result, eps...)
	}
	return result, nil
}

func (l *gatewayPodTargetLister) getRuleProbes(ctx context.Context, rule v1alpha1.IngressRule, sslOpt v1alpha1.HTTPOption) ([]status.ProbeTarget, error) {
	gatewayConfig := config.FromContext(ctx).Gateway
	service := gatewayConfig.Gateways[rule.Visibility].Service

	eps, err := l.endpointsLister.Endpoints(service.Namespace).Get(service.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	targets := make([]status.ProbeTarget, 0, len(eps.Subsets))
	foundTargets := 0
	for _, sub := range eps.Subsets {
		scheme := "http"
		// Istio uses "http2" for the http port
		matchSchemes := sets.NewString("http", "http2")
		if rule.Visibility == v1alpha1.IngressVisibilityExternalIP && sslOpt == v1alpha1.HTTPOptionRedirected {
			scheme = "https"
			matchSchemes = sets.NewString("https")
		}
		pt := status.ProbeTarget{PodIPs: sets.NewString()}

		portNumber := sub.Ports[0].Port
		for _, port := range sub.Ports {
			if matchSchemes.Has(port.Name) {
				// Prefer to match the name exactly
				portNumber = port.Port
				break
			}
			if port.AppProtocol != nil && matchSchemes.Has(*port.AppProtocol) {
				portNumber = port.Port
			}
		}
		pt.PodPort = strconv.Itoa(int(portNumber))

		for _, address := range sub.Addresses {
			pt.PodIPs.Insert(address.IP)
		}
		foundTargets += len(pt.PodIPs)

		pt.URLs = domainsToURL(rule.Hosts, scheme)
		targets = append(targets, pt)
	}
	if foundTargets == 0 {
		return nil, fmt.Errorf("no gateway pods available")
	}
	return targets, nil
}

func domainsToURL(domains []string, scheme string) []*url.URL {
	urls := make([]*url.URL, 0, len(domains))
	for _, domain := range domains {
		url := &url.URL{
			Scheme: scheme,
			Host:   domain,
			Path:   "/",
		}
		urls = append(urls, url)
	}
	return urls
}
