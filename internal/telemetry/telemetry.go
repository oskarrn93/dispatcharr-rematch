// Package telemetry exports dispatcharr-rematch's metrics and logs over
// OTLP/HTTP to server-observability's otel-collector, modelled on
// aws-route53-dyndns's telemetry package.
//
// It is off unless the TELEMETRY_ENABLED feature flag (or -telemetry) is set;
// disabled, every instrument is a no-op and logs only go to stdout. The
// endpoint comes from the standard OTEL_EXPORTER_OTLP_ENDPOINT, defaulting to
// DefaultEndpoint.
//
// Each cron run is a fresh process, so counters start from zero every run: the
// exported value of e.g. dispatcharr_rematch_channels_rematched_total is that
// run's count, not a lifetime total. The Grafana dashboard
// (server-observability/grafana/dashboards/dispatcharr-rematch.json) reads
// them that way, the same as aws-route53-dyndns.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

const (
	ServiceName     = "dispatcharr_rematch"
	DefaultEndpoint = "http://172.17.0.1:4318"
)

type Telemetry struct {
	Logger *slog.Logger

	SuccessfulRuns     metric.Int64Counter
	FailedRuns         metric.Int64Counter
	ChannelsChecked    metric.Int64Counter
	ChannelsRematched  metric.Int64Counter
	ChannelsUnmatched  metric.Int64Counter
	M3URefreshFailures metric.Int64Counter
	StreamsHealthy     metric.Int64Gauge
	StreamsStale       metric.Int64Gauge
	M3URefreshDuration metric.Float64Gauge
	RunDuration        metric.Float64Gauge
	LastRunTimestamp   metric.Float64Gauge
	LastSuccessTime    metric.Float64Gauge

	shutdown []func(context.Context) error
}

// New sets up telemetry. With enabled false, it returns no-op instruments and
// a stdout-only logger.
func New(ctx context.Context, enabled bool, version string) (*Telemetry, error) {
	stdout := slog.NewTextHandler(os.Stdout, nil)
	t := &Telemetry{Logger: slog.New(stdout)}
	var meter metric.Meter = noop.NewMeterProvider().Meter(ServiceName)

	if enabled {
		res := resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(version),
		)

		metricOpts, logOpts := []otlpmetrichttp.Option{}, []otlploghttp.Option{}
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
			if os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" {
				metricOpts = append(metricOpts, otlpmetrichttp.WithEndpointURL(DefaultEndpoint+"/v1/metrics"))
			}
			if os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") == "" {
				logOpts = append(logOpts, otlploghttp.WithEndpointURL(DefaultEndpoint+"/v1/logs"))
			}
		}

		metricExporter, err := otlpmetrichttp.New(ctx, metricOpts...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		// A long interval: metrics are exported once, by Shutdown at the end of the run.
		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(time.Hour))),
		)

		logExporter, err := otlploghttp.New(ctx, logOpts...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP log exporter: %w", err)
		}
		loggerProvider := sdklog.NewLoggerProvider(
			sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		)

		meter = meterProvider.Meter(ServiceName)
		t.Logger = slog.New(slog.NewMultiHandler(
			stdout,
			otelslog.NewHandler(ServiceName, otelslog.WithLoggerProvider(loggerProvider)),
		))
		t.shutdown = []func(context.Context) error{meterProvider.Shutdown, loggerProvider.Shutdown}
	}

	var errs []error
	counter := func(name, description string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(description))
		errs = append(errs, err)
		return c
	}
	intGauge := func(name, description string) metric.Int64Gauge {
		g, err := meter.Int64Gauge(name, metric.WithDescription(description))
		errs = append(errs, err)
		return g
	}
	secondsGauge := func(name, description string) metric.Float64Gauge {
		g, err := meter.Float64Gauge(name, metric.WithDescription(description), metric.WithUnit("s"))
		errs = append(errs, err)
		return g
	}

	t.SuccessfulRuns = counter("dispatcharr_rematch_successful_runs", "Number of successful runs")
	t.FailedRuns = counter("dispatcharr_rematch_failed_runs", "Number of failed runs")
	t.ChannelsChecked = counter("dispatcharr_rematch_channels_checked", "Channels inspected for stale/missing streams")
	t.ChannelsRematched = counter("dispatcharr_rematch_channels_rematched", "Channel stream slots replaced with a healthy stream")
	t.ChannelsUnmatched = counter("dispatcharr_rematch_channels_unmatched", "Stale/missing stream slots with no match above the threshold")
	t.M3URefreshFailures = counter("dispatcharr_rematch_m3u_refresh_failures", "M3U accounts whose refresh ended in error")
	t.StreamsHealthy = intGauge("dispatcharr_rematch_streams_healthy", "Streams not marked stale after the M3U refresh")
	t.StreamsStale = intGauge("dispatcharr_rematch_streams_stale", "Streams marked stale after the M3U refresh")
	t.M3URefreshDuration = secondsGauge("dispatcharr_rematch_m3u_refresh_duration", "Time spent waiting for the M3U refresh")
	t.RunDuration = secondsGauge("dispatcharr_rematch_run_duration", "Total run time")
	t.LastRunTimestamp = secondsGauge("dispatcharr_rematch_last_run_timestamp", "Unix time the last run finished")
	t.LastSuccessTime = secondsGauge("dispatcharr_rematch_last_success_timestamp", "Unix time the last successful run finished")

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("create instruments: %w", err)
	}
	return t, nil
}

// RecordRun records the outcome of a whole run.
func (t *Telemetry) RecordRun(ctx context.Context, success bool, started time.Time) {
	now := time.Now()
	if success {
		t.SuccessfulRuns.Add(ctx, 1)
		t.LastSuccessTime.Record(ctx, float64(now.Unix()))
	} else {
		t.FailedRuns.Add(ctx, 1)
	}
	t.RunDuration.Record(ctx, now.Sub(started).Seconds())
	t.LastRunTimestamp.Record(ctx, float64(now.Unix()))
}

// Shutdown flushes and stops the exporters. Errors are logged, never
// returned: a run shouldn't fail because the collector is down.
func (t *Telemetry) Shutdown(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, shutdown := range t.shutdown {
		if err := shutdown(ctx); err != nil {
			t.Logger.Warn("failed to flush telemetry", "error", err)
		}
	}
}
