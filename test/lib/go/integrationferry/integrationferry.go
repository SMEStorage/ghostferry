package integrationferry

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/Shopify/ghostferry"
	"github.com/Shopify/ghostferry/testhelpers"
)

const (
	// These should be kept in sync with ghostferry.rb
	portEnvName    string        = "GHOSTFERRY_INTEGRATION_PORT"
	timeout        time.Duration = 30 * time.Second
	maxMessageSize int           = 256
)

const (
	// These should be kept in sync with ghostferry.rb

	// Could only be sent once by the main thread
	StatusReady                  string = "READY"
	StatusBinlogStreamingStarted string = "BINLOG_STREAMING_STARTED"
	StatusRowCopyCompleted       string = "ROW_COPY_COMPLETED"
	StatusDone                   string = "DONE"

	// Could be sent by multiple goroutines in parallel
	StatusBeforeRowCopy     string = "BEFORE_ROW_COPY"
	StatusAfterRowCopy      string = "AFTER_ROW_COPY"
	StatusBeforeBinlogApply string = "BEFORE_BINLOG_APPLY"
	StatusAfterBinlogApply  string = "AFTER_BINLOG_APPLY"
)

type IntegrationFerry struct {
	*ghostferry.Ferry
}

// =========================================
// Code for integration server communication
// =========================================

func (f *IntegrationFerry) SendStatusAndWaitUntilContinue(status string, data ...string) error {
	integrationPort := os.Getenv(portEnvName)
	if integrationPort == "" {
		return fmt.Errorf("environment variable %s must be specified", portEnvName)
	}

	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.PostForm(fmt.Sprintf("http://localhost:%s", integrationPort), url.Values{
		"status": []string{status},
		"data":   data,
	})

	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned invalid status: %d", resp.StatusCode)
	}

	return nil
}

// Method override for Start in order to send status to the integration
// server.
func (f *IntegrationFerry) Start() error {
	f.Ferry.DataIterator.AddBatchListener(func(rowBatch *ghostferry.RowBatch) error {
		return f.SendStatusAndWaitUntilContinue(StatusBeforeRowCopy, rowBatch.TableSchema().Name)
	})

	f.Ferry.BinlogStreamer.AddEventListener(func(events []ghostferry.DMLEvent) error {
		return f.SendStatusAndWaitUntilContinue(StatusBeforeBinlogApply)
	})

	err := f.Ferry.Start()
	if err != nil {
		return err
	}

	f.Ferry.DataIterator.AddBatchListener(func(rowBatch *ghostferry.RowBatch) error {
		return f.SendStatusAndWaitUntilContinue(StatusAfterRowCopy, rowBatch.TableSchema().Name)
	})

	f.Ferry.BinlogStreamer.AddEventListener(func(events []ghostferry.DMLEvent) error {
		return f.SendStatusAndWaitUntilContinue(StatusAfterBinlogApply)
	})

	return nil
}

// ===========================================
// Code to handle an almost standard Ferry run
// ===========================================
func (f *IntegrationFerry) Main() error {
	var err error

	err = f.SendStatusAndWaitUntilContinue(StatusReady)
	if err != nil {
		return err
	}

	err = f.Initialize()
	if err != nil {
		return err
	}

	err = f.Start()
	if err != nil {
		return err
	}

	err = f.SendStatusAndWaitUntilContinue(StatusBinlogStreamingStarted)
	if err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		f.Run()
	}()

	f.WaitUntilRowCopyIsComplete()
	err = f.SendStatusAndWaitUntilContinue(StatusRowCopyCompleted)
	if err != nil {
		return err
	}

	// TODO: this method should return errors rather than calling
	// the error handler to panic directly.
	f.FlushBinlogAndStopStreaming()
	wg.Wait()

	return f.SendStatusAndWaitUntilContinue(StatusDone)
}

func NewStandardConfig() (*ghostferry.Config, error) {
	config := &ghostferry.Config{
		Source: ghostferry.DatabaseConfig{
			Host:      "127.0.0.1",
			Port:      uint16(29291),
			User:      "root",
			Pass:      "",
			Collation: "utf8mb4_unicode_ci",
			Params: map[string]string{
				"charset": "utf8mb4",
			},
		},

		Target: ghostferry.DatabaseConfig{
			Host:      "127.0.0.1",
			Port:      uint16(29292),
			User:      "root",
			Pass:      "",
			Collation: "utf8mb4_unicode_ci",
			Params: map[string]string{
				"charset": "utf8mb4",
			},
		},

		AutomaticCutover: true,
		TableFilter: &testhelpers.TestTableFilter{
			DbsFunc:    testhelpers.DbApplicabilityFilter([]string{"gftest"}),
			TablesFunc: nil,
		},

		DumpStateToStdoutOnSignal: true,
	}

	resumeStateJSON, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	if len(resumeStateJSON) > 0 {
		config.StateToResumeFrom = &ghostferry.SerializableState{}
		err = json.Unmarshal(resumeStateJSON, config.StateToResumeFrom)
		if err != nil {
			return nil, err
		}
	}

	return config, config.ValidateConfig()
}
