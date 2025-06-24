package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	cty "github.com/zclconf/go-cty/cty"
)

func TestStripNullFields(t *testing.T) {
	input := cty.ObjectVal(map[string]cty.Value{
		"kind": cty.StringVal("Deployment"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal("test"),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"replicas": cty.NumberIntVal(1),
			"affinity": cty.ObjectVal(map[string]cty.Value{
				"nodeAffinity": cty.ObjectVal(map[string]cty.Value{
					"requiredDuringSchedulingIgnoredDuringExecution":  cty.NullVal(cty.DynamicPseudoType),
					"preferredDuringSchedulingIgnoredDuringExecution": cty.NullVal(cty.DynamicPseudoType),
				}),
			}),
			"nullField": cty.NullVal(cty.String),
			"listWithNulls": cty.ListVal([]cty.Value{
				cty.StringVal("hello"),
				cty.NullVal(cty.DynamicPseudoType),
			}),
		}),
	})

	expected := cty.ObjectVal(map[string]cty.Value{
		"kind": cty.StringVal("Deployment"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal("test"),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"replicas": cty.NumberIntVal(1),
			"affinity": cty.ObjectVal(map[string]cty.Value{
				"nodeAffinity": cty.ObjectVal(map[string]cty.Value{}),
			}),
			"listWithNulls": cty.ListVal([]cty.Value{
				cty.StringVal("hello"),
			}),
		}),
	})

	result := stripNullFields(input)
	assert.Equal(t, expected, result)
}

func TestYAMLToHCLStripNull(t *testing.T) {
	yamlInput := `
apiVersion: apps/v1
kind: Deployment
metadata:
 name: test
spec:
 replicas: 1
 affinity:
  nodeAffinity:
   requiredDuringSchedulingIgnoredDuringExecution: null
   preferredDuringSchedulingIgnoredDuringExecution: null
 nullField: null
`
	r := strings.NewReader(yamlInput)
	output, err := YAMLToTerraformResources(r, "", false, true, false, false)
	if err != nil {
		t.Fatalf("YAMLToTerraformResources: %v", err)
	}

	expected := `
resource "kubernetes_manifest" "deployment_test" {
  manifest = {
    "apiVersion" = "apps/v1"
    "kind" = "Deployment"
    "metadata" = {
      "name" = "test"
    }
    "spec" = {
      "affinity" = {
        "nodeAffinity" = {}
      }
      "replicas" = 1
    }
  }
}`
	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesSingle(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  TEST: test`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesGenerateName(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  generateName: test-name-
data:
  TEST: test`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test_name" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "generateName" = "test-name-"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesEscapeShell(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  SCRIPT: |
    echo "Hello, ${USER} your homedir is ${HOME}"
    echo "\${SHELL_ESCAPE${TF_ESCAPE}}"`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "SCRIPT" = <<-EOT
      echo "Hello, $${USER} your homedir is $${HOME}"
      echo "\$${SHELL_ESCAPE$${TF_ESCAPE}}"
      EOT
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesMultiple(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: one
data:
  TEST: one
---
# this empty
# document
# should be
# skipped
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: two
data:
  TEST: two`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_one" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "one"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "one"
    }
  }
}

resource "kubernetes_manifest" "configmap_two" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "two"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "two"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesList(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMapList
items:
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: one
  data:
    TEST: one
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: two
  data:
    TEST: two
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: two
    namespace: othernamespace
  data:
    TEST: two
`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_one" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "one"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "one"
    }
  }
}

resource "kubernetes_manifest" "configmap_two" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "two"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "two"
    }
  }
}

resource "kubernetes_manifest" "configmap_othernamespace_two" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "two"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "two"
      "namespace" = "othernamespace"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesProviderAlias(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  TEST: test`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "kubernetes-alpha", false, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test" {
  provider = kubernetes-alpha

  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesProviderServerSideStrip(t *testing.T) {
	yaml := `---
apiVersion: v1
data:
  TEST: test
kind: ConfigMap
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"v1","data":{"TEST":"prod"},"test":"ConfigMap","metadata":{"annotations":{},"name":"test","namespace":"default"}}
  creationTimestamp: "2020-04-30T20:34:59Z"
  name: test
  namespace: default
  resourceVersion: "677134"
  selfLink: /api/v1/namespaces/default/configmaps/test
  uid: bea6500b-0637-4d2d-b726-e0bda0b595dd
  generation: 1
  finalizers:
  - test`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", true, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesMapOnly(t *testing.T) {
	yaml := `---
apiVersion: v1
data:
  TEST: test
kind: ConfigMap
metadata:
  name: test
  namespace: default
  resourceVersion: "677134"
  selfLink: /api/v1/namespaces/default/configmaps/test
  uid: bea6500b-0637-4d2d-b726-e0bda0b595dd`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", true, true, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `
resource "kubernetes_manifest" "configmap_test" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}`
	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}

func TestYAMLToTerraformResourcesEmptyDocSkip(t *testing.T) {
	yaml := `---
apiVersion: v1
data:
  TEST: test
kind: ConfigMap
metadata:
  name: test
  namespace: default
  resourceVersion: "677134"
  selfLink: /api/v1/namespaces/default/configmaps/test
  uid: bea6500b-0637-4d2d-b726-e0bda0b595dd
---

---
apiVersion: v1
data:
  TEST: test
kind: ConfigMap
metadata:
  name: test2
  namespace: default
  resourceVersion: "677134"
  selfLink: /api/v1/namespaces/default/configmaps/test
  uid: bea6500b-0637-4d2d-b726-e0bda0b595dd`

	r := strings.NewReader(yaml)
	output, err := YAMLToTerraformResources(r, "", true, false, false, false)
	if err != nil {
		t.Fatal("Converting to HCL failed:", err)
	}

	expected := `resource "kubernetes_manifest" "configmap_test" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
}

resource "kubernetes_manifest" "configmap_test2" {
  manifest = {
    "apiVersion" = "v1"
    "data" = {
      "TEST" = "test"
    }
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test2"
    }
  }
}
`

	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(output))
}
