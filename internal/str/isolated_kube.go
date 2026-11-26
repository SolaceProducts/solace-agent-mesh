// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	serviceAccountTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	kubeRequestTimeout      = 30 * time.Second
	kubeErrorBodyLimit      = 2048
)

var (
	errKubeNotFound      = errors.New("kubernetes object not found")
	errKubeAlreadyExists = errors.New("kubernetes object already exists")
)

type kubeStatusError struct {
	method string
	path   string
	code   int
	body   string
}

func (e *kubeStatusError) Error() string {
	return fmt.Sprintf("kubernetes api %s %s: status %d: %s", e.method, e.path, e.code, e.body)
}

type kubeClient struct {
	http      *http.Client
	baseURL   string
	tokenFile string
}

func newInClusterKubeClient() (*kubeClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("not running in a Kubernetes pod: KUBERNETES_SERVICE_HOST/PORT unset")
	}
	caPEM, err := os.ReadFile(serviceAccountCAFile)
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("service account CA holds no certificate")
	}
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 8,
	}
	return &kubeClient{
		http:      &http.Client{Transport: transport},
		baseURL:   "https://" + net.JoinHostPort(host, port),
		tokenFile: serviceAccountTokenFile,
	}, nil
}

func (c *kubeClient) token() (string, error) {
	if c.tokenFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("read service account token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (c *kubeClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	token, err := c.token()
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *kubeClient) do(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, kubeRequestTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kubernetes api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return statusError(method, path, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("kubernetes api %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func statusError(method, path string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		return errKubeNotFound
	case http.StatusConflict:
		return errKubeAlreadyExists
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, kubeErrorBodyLimit))
	return &kubeStatusError{method: method, path: path, code: resp.StatusCode, body: string(body)}
}

type kubeObjectMeta struct {
	Name              string               `json:"name"`
	UID               string               `json:"uid"`
	Labels            map[string]string    `json:"labels"`
	CreationTimestamp time.Time            `json:"creationTimestamp"`
	OwnerReferences   []kubeOwnerReference `json:"ownerReferences"`
}

type kubeOwnerReference struct {
	UID string `json:"uid"`
}

type kubeJobCondition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

type kubeJob struct {
	Metadata kubeObjectMeta `json:"metadata"`
	Status   struct {
		Active         int                `json:"active"`
		Succeeded      int                `json:"succeeded"`
		Failed         int                `json:"failed"`
		CompletionTime *time.Time         `json:"completionTime"`
		Conditions     []kubeJobCondition `json:"conditions"`
	} `json:"status"`
}

type kubeContainerState struct {
	Running *struct {
		StartedAt time.Time `json:"startedAt"`
	} `json:"running"`
	Terminated *struct {
		ExitCode   int       `json:"exitCode"`
		Reason     string    `json:"reason"`
		StartedAt  time.Time `json:"startedAt"`
		FinishedAt time.Time `json:"finishedAt"`
	} `json:"terminated"`
	Waiting *struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"waiting"`
}

type kubePodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type kubePod struct {
	Metadata kubeObjectMeta `json:"metadata"`
	Status   struct {
		Phase             string             `json:"phase"`
		Reason            string             `json:"reason"`
		Message           string             `json:"message"`
		Conditions        []kubePodCondition `json:"conditions"`
		ContainerStatuses []struct {
			Name  string             `json:"name"`
			State kubeContainerState `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type kubeList[T any] struct {
	Items []T `json:"items"`
}

func jobsPath(namespace string) string {
	return "/apis/batch/v1/namespaces/" + url.PathEscape(namespace) + "/jobs"
}

func coreNamespacePath(namespace string) string {
	return "/api/v1/namespaces/" + url.PathEscape(namespace)
}

func secretsPath(namespace string) string {
	return coreNamespacePath(namespace) + "/secrets"
}

func podsPath(namespace string) string {
	return coreNamespacePath(namespace) + "/pods"
}

func (c *kubeClient) createJob(ctx context.Context, namespace string, job map[string]any) (*kubeJob, error) {
	var out kubeJob
	if err := c.do(ctx, http.MethodPost, jobsPath(namespace), job, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *kubeClient) getJob(ctx context.Context, namespace, name string) (*kubeJob, error) {
	var out kubeJob
	if err := c.do(ctx, http.MethodGet, jobsPath(namespace)+"/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *kubeClient) listJobs(ctx context.Context, namespace, selector string) ([]kubeJob, error) {
	var out kubeList[kubeJob]
	path := jobsPath(namespace) + "?labelSelector=" + url.QueryEscape(selector)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *kubeClient) deleteJob(ctx context.Context, namespace, name string) error {
	path := jobsPath(namespace) + "/" + url.PathEscape(name) + "?propagationPolicy=Background"
	return ignoreNotFound(c.do(ctx, http.MethodDelete, path, nil, nil))
}

func (c *kubeClient) createSecret(ctx context.Context, namespace string, secret map[string]any) error {
	return c.do(ctx, http.MethodPost, secretsPath(namespace), secret, nil)
}

func (c *kubeClient) deleteSecret(ctx context.Context, namespace, name string) error {
	return ignoreNotFound(c.do(ctx, http.MethodDelete, secretsPath(namespace)+"/"+url.PathEscape(name), nil, nil))
}

func (c *kubeClient) listPods(ctx context.Context, namespace, selector string) ([]kubePod, error) {
	var out kubeList[kubePod]
	path := podsPath(namespace) + "?labelSelector=" + url.QueryEscape(selector)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *kubeClient) ownImage(ctx context.Context, namespace, podName, container string) (string, error) {
	var pod struct {
		Spec struct {
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
	}
	if err := c.do(ctx, http.MethodGet, podsPath(namespace)+"/"+url.PathEscape(podName), nil, &pod); err != nil {
		return "", err
	}
	for _, ct := range pod.Spec.Containers {
		if ct.Name == container {
			return ct.Image, nil
		}
	}
	if len(pod.Spec.Containers) == 0 {
		return "", fmt.Errorf("pod %s has no containers", podName)
	}
	return pod.Spec.Containers[0].Image, nil
}

func (c *kubeClient) podLogs(ctx context.Context, namespace, pod string, follow bool) (io.ReadCloser, error) {
	path := podsPath(namespace) + "/" + url.PathEscape(pod) + "/log?container=" + isolatedContainerName
	if follow {
		path += "&follow=true"
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kubernetes api GET %s: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, statusError(http.MethodGet, path, resp)
	}
	return resp.Body, nil
}

func ignoreNotFound(err error) error {
	if errors.Is(err, errKubeNotFound) {
		return nil
	}
	return err
}
