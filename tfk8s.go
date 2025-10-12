package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	flag "github.com/spf13/pflag"

	cty "github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	yaml "sigs.k8s.io/yaml"

	"github.com/jrhouston/tfk8s/contrib/hashicorp/terraform"
)

// toolVersion is the version that gets printed when you run --version
var toolVersion string

// resourceType is the type of Terraform resource
var resourceType = "kubernetes_manifest"

// ignoreMetadata is the list of metadata fields to strip
// when --strip is supplied
var ignoreMetadata = []string{
	"creationTimestamp",
	"resourceVersion",
	"selfLink",
	"uid",
	"managedFields",
	"finalizers",
	"generation",
}

// ignoreAnnotations is the list of annotations to strip
// when --strip is supplied
var ignoreAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
}

var yamlSeparator = "\n---"

// stripNullFields removes any field that is null
// This is a simplified version that only processes objects and avoids list processing
// to prevent type inconsistency issues with complex manifests like Istio
func stripNullFields(val cty.Value) cty.Value {
	if val.Type().IsObjectType() || val.Type().IsMapType() {
		m := val.AsValueMap()
		newMap := make(map[string]cty.Value)
		for k, v := range m {
			if !v.IsNull() {
				// Only recursively process if it's an object/map, not lists
				if v.Type().IsObjectType() || v.Type().IsMapType() {
					newMap[k] = stripNullFields(v)
				} else {
					newMap[k] = v
				}
			}
		}
		return cty.ObjectVal(newMap)
	}

	// For lists, we only remove null elements without modifying their structure
	if val.Type().IsListType() || val.Type().IsSetType() || val.Type().IsTupleType() {
		slice := val.AsValueSlice()
		newSlice := make([]cty.Value, 0)
		for _, v := range slice {
			if !v.IsNull() {
				newSlice = append(newSlice, v)
			}
		}
		return cty.ListVal(newSlice)
	}

	return val
}

// stripServerSideFields removes server-side fields and optionally null fields
func stripServerSideFields(doc cty.Value, stripNull bool) cty.Value {
	m := doc.AsValueMap()

	// Strip server-side metadata (only if metadata exists and is not null)
	if metadataVal, ok := m["metadata"]; ok && !metadataVal.IsNull() {
		metadata := metadataVal.AsValueMap()
		for _, f := range ignoreMetadata {
			delete(metadata, f)
		}
		if v, ok := metadata["annotations"]; ok && !v.IsNull() {
			annotations := v.AsValueMap()
			for _, a := range ignoreAnnotations {
				delete(annotations, a)
			}
			if len(annotations) == 0 {
				delete(metadata, "annotations")
			} else {
				metadata["annotations"] = cty.ObjectVal(annotations)
			}
		}
		if ns, ok := metadata["namespace"]; ok && ns.AsString() == "default" {
			delete(metadata, "namespace")
		}
		m["metadata"] = cty.ObjectVal(metadata)
	}

	// Strip finalizer from spec
	if v, ok := m["spec"]; ok {
		mm := v.AsValueMap()
		delete(mm, "finalizers")
		m["spec"] = cty.ObjectVal(mm)
	}

	// Strip status field
	delete(m, "status")

	// Strip null fields if requested
	if stripNull {
		return stripNullFields(cty.ObjectVal(m))
	}

	return cty.ObjectVal(m)
}

// snakify converts "a-String LIKE this" to "a_string_like_this"
func snakify(s string) string {
	re := regexp.MustCompile(`\W`)
	return strings.ToLower(re.ReplaceAllString(s, "_"))
}

// escapeShellVars converts "${}" to "$${}" to prevent Terraform interpolation
func escapeShellVars(s string) string {
	r := regexp.MustCompile(`(\${.*?)`)
	return r.ReplaceAllString(s, `$$$1`)
}

// yamlToHCL converts a single YAML document to Terraform HCL
func yamlToHCL(
	doc cty.Value, providerAlias string,
	stripServerSide bool, stripNull bool, mapOnly bool, stripKeyQuotes bool,
) (string, error) {
	m := doc.AsValueMap()
	docs := []cty.Value{doc}
	if strings.HasSuffix(m["kind"].AsString(), "List") {
		docs = m["items"].AsValueSlice()
	}

	hcl := ""
	for i, doc := range docs {
		mm := doc.AsValueMap()
		kind := mm["kind"].AsString()
		
		var metadata map[string]cty.Value
		var namespace string
		var name string
		
		if metadataVal, ok := mm["metadata"]; ok && !metadataVal.IsNull() {
			metadata = metadataVal.AsValueMap()
			if v, ok := metadata["namespace"]; ok {
				namespace = v.AsString()
			}

			if n, ok := metadata["name"]; ok {
				name = n.AsString()
			} else if n, ok := metadata["generateName"]; ok {
				name = n.AsString()
				if name[len(name)-1] == '-' {
					name = name[:len(name)-1]
				}
			}
		}

		resourceName := kind
		if namespace != "" && namespace != "default" {
			resourceName = resourceName + "_" + namespace
		}
		resourceName = resourceName + "_" + name
		resourceName = snakify(resourceName)

		if stripServerSide || stripNull {
			doc = stripServerSideFields(doc, stripNull)
		}
		s := terraform.FormatValue(doc, 0, stripKeyQuotes)
		s = escapeShellVars(s)

		if mapOnly {
			hcl += fmt.Sprintf("%v\n", s)
		} else {
			hcl += fmt.Sprintf("resource %q %q {\n", resourceType, resourceName)
			if providerAlias != "" {
				hcl += fmt.Sprintf("  provider = %v\n\n", providerAlias)
			}
			hcl += fmt.Sprintf("  manifest = %v\n", strings.ReplaceAll(s, "\n", "\n  "))
			hcl += "}\n"
		}
		if i != len(docs)-1 {
			hcl += "\n"
		}
	}

	return hcl, nil
}

// YAMLToTerraformResources converts YAML input to Terraform resources
func YAMLToTerraformResources(
	r io.Reader, providerAlias string, stripServerSide bool,
	stripNull bool, mapOnly bool, stripKeyQuotes bool,
) (string, error) {
	hcl := ""

	buf := bytes.Buffer{}
	_, err := buf.ReadFrom(r)
	if err != nil {
		return "", err
	}

	count := 0
	manifest := buf.String()
	docs := strings.Split(manifest, yamlSeparator)
	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			// some manifests have empty documents
			continue
		}

		var b []byte
		b, err = yaml.YAMLToJSON([]byte(doc))
		if err != nil {
			return "", err
		}

		t, err := ctyjson.ImpliedType(b)
		if err != nil {
			return "", err
		}

		doc, err := ctyjson.Unmarshal(b, t)
		if err != nil {
			return "", err
		}

		if doc.IsNull() {
			// skip empty YAML docs
			continue
		}

		if !doc.Type().IsObjectType() {
			return "", fmt.Errorf("the manifest must be a YAML document")
		}

		formatted, err := yamlToHCL(doc, providerAlias, stripServerSide, stripNull, mapOnly, stripKeyQuotes)
		if err != nil {
			return "", fmt.Errorf("error converting YAML to HCL: %s", err)
		}

		if count > 0 {
			hcl += "\n"
		}
		hcl += formatted
		count++
	}

	return hcl, nil
}

func capturePanic() {
	if r := recover(); r != nil {
		fmt.Printf(
			"panic: %s\n\n%s\n\n"+
				"⚠️  Oh no! Looks like your manifest caused tfk8s to crash.\n\n"+
				"Please open a GitHub issue and include your manifest YAML with the stack trace above,\n"+
				"or ping me on slack and I'll try and fix it!\n\n"+
				"GitHub: https://github.com/jrhouston/tfk8s/issues\n"+
				"Slack: #terraform-providers on https://kubernetes.slack.com\n\n"+
				"- Thanks, @jrhouston\n\n",
			r, debug.Stack())
	}
}

func main() {
	defer capturePanic()

	infile := flag.StringP("file", "f", "-", "Input file containing Kubernetes YAML manifests")
	outfile := flag.StringP("output", "o", "-", "Output file to write Terraform config")
	providerAlias := flag.StringP("provider", "p", "", "Provider alias to populate the `provider` attribute")
	stripServerSide := flag.BoolP("strip", "s", false, "Strip out server side fields - use if you are piping from kubectl get")
	stripNull := flag.BoolP("strip-null", "n", false, "Strip out fields with null values")
	mapOnly := flag.BoolP("map-only", "M", false, "Output only an HCL map structure")
	stripKeyQuotes := flag.BoolP("strip-key-quotes", "Q", false, "Strip out quotes from HCL map keys unless they are required.")
	version := flag.BoolP("version", "V", false, "Show tool version")
	flag.Parse()

	if *version {
		fmt.Println(toolVersion)
		os.Exit(0)
	}

	var file *os.File
	if *infile == "-" {
		file = os.Stdin
	} else {
		var err error
		file, err = os.Open(*infile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\r\n", err.Error())
			os.Exit(1)
		}
		defer file.Close()
	}

	hcl, err := YAMLToTerraformResources(file, *providerAlias, *stripServerSide, *stripNull, *mapOnly, *stripKeyQuotes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\r\n", err.Error())
		os.Exit(1)
	}

	if *outfile == "-" {
		fmt.Print(hcl)
	} else {
		err := os.WriteFile(*outfile, []byte(hcl), 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\r\n", err.Error())
			os.Exit(1)
		}
	}
}
