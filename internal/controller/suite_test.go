package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/stevendborrelli/records-controller/api/v1alpha1"
)

// k8s is a direct, uncached client to the envtest API server.
var k8s client.Client

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(os.Getenv("VERBOSE") != "")))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		dir, err := binaryAssets()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot find envtest binaries; install them with setup-envtest or set KUBEBUILDER_ASSETS: %v\n", err)
			return 1
		}
		env.BinaryAssetsDirectory = dir
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start envtest: %v\n", err)
		return 1
	}
	defer env.Stop() //nolint:errcheck // Best effort.

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create client: %v\n", err)
		return 1
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create manager: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Setup(ctx, mgr); err != nil {
		fmt.Fprintf(os.Stderr, "cannot set up controllers: %v\n", err)
		return 1
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "manager exited: %v\n", err)
		}
	}()

	return m.Run()
}

// binaryAssets returns the newest envtest binaries installed by setup-envtest.
func binaryAssets() (string, error) {
	base, err := envtest.SetupEnvtestDefaultBinaryAssetsDirectory()
	if err != nil {
		return "", err
	}
	dirs, err := filepath.Glob(filepath.Join(base, "*-"+goruntime.GOOS+"-"+goruntime.GOARCH))
	if err != nil || len(dirs) == 0 {
		return "", fmt.Errorf("no binaries under %s", base)
	}
	slices.Sort(dirs)
	return dirs[len(dirs)-1], nil
}

// namespace creates a namespace for a test.
func namespace(t *testing.T) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "test-"}}
	if err := k8s.Create(context.Background(), ns); err != nil {
		t.Fatalf("cannot create namespace: %v", err)
	}
	return ns.Name
}

// eventually polls fn until it returns nil, failing the test on timeout.
func eventually(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = fn(); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %v", what, err)
}

// waitForCondition waits for an object's condition to have the given status
// and reason, and returns the object.
func waitForCondition[T client.Object](t *testing.T, obj T, conds func(T) []metav1.Condition, ct string, status metav1.ConditionStatus, reason string) T {
	t.Helper()
	eventually(t, fmt.Sprintf("%s %s=%s (%s)", obj.GetName(), ct, status, reason), func() error {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		c := meta.FindStatusCondition(conds(obj), ct)
		if c == nil {
			return fmt.Errorf("no %s condition", ct)
		}
		if c.Status != status || c.Reason != reason {
			return fmt.Errorf("%s is %s (%s): %s", ct, c.Status, c.Reason, c.Message)
		}
		return nil
	})
	return obj
}

func recordConds(r *v1alpha1.Record) []metav1.Condition         { return r.Status.Conditions }
func schemaConds(s *v1alpha1.Schema) []metav1.Condition         { return s.Status.Conditions }
func setConds(s *v1alpha1.RecordSet) []metav1.Condition         { return s.Status.Conditions }
func clusterConds(s *v1alpha1.ClusterSchema) []metav1.Condition { return s.Status.Conditions }

// update re-reads obj, applies mutate, and updates it, retrying on conflict.
// The controllers write status concurrently, and the API server checks
// resourceVersion before admission validation, so updating a stale copy fails
// with a conflict instead of the validation error under test.
func update[T client.Object](obj T, mutate func(T)) error {
	return retryOnConflict(obj, mutate, func(ctx context.Context) error { return k8s.Update(ctx, obj) })
}

// updateStatus is update for the status subresource.
func updateStatus[T client.Object](obj T, mutate func(T)) error {
	return retryOnConflict(obj, mutate, func(ctx context.Context) error { return k8s.Status().Update(ctx, obj) })
}

func retryOnConflict[T client.Object](obj T, mutate func(T), write func(context.Context) error) error {
	key := client.ObjectKeyFromObject(obj)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Decoding into a populated struct keeps fields the response omits,
		// so start from zero to get exactly what the API server stores.
		reflect.ValueOf(obj).Elem().SetZero()
		if err := k8s.Get(context.Background(), key, obj); err != nil {
			return err
		}
		mutate(obj)
		return write(context.Background())
	})
}

// mustReject asserts that an API call was rejected with a message containing
// want.
func mustReject(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want the API server to reject the request with %q, but it was accepted", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want rejection containing %q, got: %v", want, err)
	}
}
