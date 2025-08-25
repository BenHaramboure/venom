package venom

import (
    "log"
    "fmt"
	"os"
	"time"
	"net/url"
	"bytes"
	"strings"
	_ "embed"
	"encoding/json"
	"text/template"
	"github.com/pkg/errors"
)

//go:embed venom_output.html
var templateHTML string

type TestsHTML struct {
	Tests     Tests  `json:"tests"`
	JSONValue string `json:"jsonValue"`
}

func buildGrafanaURL(baseURL string, fromDate, toDate int64, namespace, requestId string) string {
    expr := fmt.Sprintf("{namespace=\"%s\"}", namespace)
    if requestId != "" {
    	expr += fmt.Sprintf(" |= \"%s\"", requestId)
    }
    panes := map[string]interface{}{
        "PANE": map[string]interface{}{
            "datasource": os.Getenv("LOGS_STREAM_ID"),
            "queries": []map[string]interface{}{
                {
                    "refId":     "A",
                    "expr":      expr,
                    "queryType": "range",
                    "datasource": map[string]interface{}{
                        "type": os.Getenv("LOGS_STREAM_NAME"),
                        "uid":  os.Getenv("LOGS_STREAM_ID"),
                    },
                },
            },
            "range": map[string]string{
                "from": fmt.Sprintf("%d", fromDate),
                "to":   fmt.Sprintf("%d", toDate),
            },
        },
    }

    panesJSON, err := json.Marshal(panes)
    if err != nil {
        log.Printf("an error occurred during json.Marshal: %s", err)
    }

    params := url.Values{}
    params.Set("schemaVersion", "1")
    params.Set("panes", string(panesJSON))
    params.Set("orgId", "1")

    return fmt.Sprintf("%s?%s", baseURL, params.Encode())
}

func buildOpenSearchURL(baseURL, fromDate, toDate, namespace, requestId string) string {
    var query string

    if namespace != "" {
        query = fmt.Sprintf("source:%s*", namespace)
    }

    if requestId != "" {
        if query != "" {
            query += " AND "
        }
        query += fmt.Sprintf("\"%s\"", requestId)
    }

    encodedQuery := url.QueryEscape(query)

    return fmt.Sprintf("%s/app/data-explorer/discover#?_a=(discover:(columns:!(application_name,client_ip,domain_id,full_msg),isDirty:!f,sort:!()),metadata:(indexPattern:'%s',view:discover))&_g=(filters:!(),refreshInterval:(pause:!t,value:0),time:(from:'%s',to:'%s'))&_q=(filters:!(),query:(language:kuery,query:'%s'))",
        baseURL,
        os.Getenv("LOGS_STREAM_ID"),
        fromDate,
        toDate,
        encodedQuery)
}

func outputHTML(testsResult *Tests) ([]byte, error) {
	var buf bytes.Buffer

    hasLogsPlatform := os.Getenv("HAS_LOGS_PLATFORM") == "true"
    logsPlatformBaseURL := os.Getenv("LOGS_PLATFORM_BASE_URL")
    namespace := os.Getenv("NAMESPACE")
    trackedHeader := os.Getenv("TRACKED_HEADER")
    if hasLogsPlatform && logsPlatformBaseURL != "" {
        for _, suite := range testsResult.TestSuites {
            for _, testCase := range suite.TestCases {
                for idx := range testCase.TestStepResults {
                    result := &testCase.TestStepResults[idx]
                    if len(result.Errors) > 0 && !result.End.IsZero() {
                        var requestIdHeader string

                        if v, ok := result.ComputedVars[fmt.Sprintf("result.headers.%s", trackedHeader)]; ok {
                            requestIdHeader = strings.TrimPrefix(strings.TrimSpace(fmt.Sprintf("%v", v)), ".")
                        } else if v, ok := result.ComputedVars[fmt.Sprintf("result.headersjson.%s", trackedHeader)]; ok {
                            requestIdHeader = strings.TrimPrefix(strings.TrimSpace(fmt.Sprintf("%v", v)), ".")
                        } else if rawHeaders, ok := result.ComputedVars["result.headers"]; ok {
                            switch headers := rawHeaders.(type) {
                            case map[string]interface{}:
                                for k, v := range headers {
                                    if strings.EqualFold(k, trackedHeader) {
                                        requestIdHeader = strings.TrimPrefix(strings.TrimSpace(fmt.Sprintf("%v", v)), ".")
                                        break
                                    }
                                }
                            case map[interface{}]interface{}:
                                for k, v := range headers {
                                    if ks, ok := k.(string); ok && strings.EqualFold(ks, trackedHeader) {
                                        requestIdHeader = strings.TrimPrefix(strings.TrimSpace(fmt.Sprintf("%v", v)), ".")
                                        break
                                    }
                                }
                            default:
                                log.Printf("unsupported headers type: %T", rawHeaders)
                            }
                        }

                        if strings.ToLower(os.Getenv("LOGS_PLATFORM_NAME")) == "opensearch" {
                            fromDate := result.End.Add(-2 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
                            toDate := result.End.UTC().Format("2006-01-02T15:04:05.000Z")
                            result.LogsUrl = buildOpenSearchURL(logsPlatformBaseURL, fromDate, toDate, namespace, requestIdHeader)
                        } else if strings.ToLower(os.Getenv("LOGS_PLATFORM_NAME")) == "grafana" {
                            fromDate := result.End.Add(-2 * time.Minute).UnixMilli()
                            toDate := result.End.UnixMilli()
                            result.LogsUrl = buildGrafanaURL(logsPlatformBaseURL, fromDate, toDate, namespace, requestIdHeader)
                        }
                    }
                }
            }
        }
    }

	testJSON, err := json.MarshalIndent(testsResult, "", " ")
	if err != nil {
		return nil, errors.Wrap(err, "unable to make json value")
	}

	testsHTML := TestsHTML{
		Tests:     *testsResult,
		JSONValue: string(testJSON),
	}
	tmpl := template.Must(template.New("reportHTML").Parse(templateHTML))
	if err := tmpl.Execute(&buf, testsHTML); err != nil {
		return nil, errors.Wrap(err, "unable to make template")
	}
	return buf.Bytes(), nil
}
