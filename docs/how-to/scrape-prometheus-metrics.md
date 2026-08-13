# How to scrape Prometheus metrics

Enable the daemon metrics listener and collect `agentlab_` counters and
histograms with Prometheus. The listener is disabled by default; you opt in by
setting `metrics_listen` to a loopback address.

For the full metric catalog, see [Prometheus metrics](../reference/metrics.md).
For listener addresses and bind rules, see
[Listeners and ports](../reference/listeners-and-ports.md).

## Prerequisites

- A running `agentlabd`.
- A Prometheus server that can reach the host loopback (through an SSH tunnel,
  a node exporter, or a sidecar).

## Steps

1. Set `metrics_listen` in `/etc/agentlab/config.yaml`. The address must be
   loopback; the daemon rejects `0.0.0.0` and other non-loopback hosts.

    ```yaml
    metrics_listen: 127.0.0.1:8847
    ```

    !!! warning "Loopback only"
        The metrics endpoint is unauthenticated. Binding it to a non-loopback
        address is rejected at config validation on purpose. Scrape it through
        a tunnel or a local relay.

2. Restart the daemon so the listener comes up.

    ```bash
    agentlabd -config /etc/agentlab/config.yaml
    ```

    Under systemd:

    ```bash
    sudo systemctl restart agentlabd.service
    ```

3. Add a scrape job to your Prometheus configuration.

    ```yaml
    scrape_configs:
      - job_name: agentlab
        scrape_interval: 30s
        static_configs:
          - targets: ['127.0.0.1:8847']
    ```

    When Prometheus runs on a different host, forward the port over SSH or use
    a node-exporter textfile collector instead of exposing the listener.

## Verify

Query the endpoint directly before you rely on Prometheus.

```bash
curl -s http://127.0.0.1:8847/metrics | grep agentlab_
```

Then confirm the daemon reports healthy.

```bash
agentlab status
```

A scrape returns counters and histograms such as
`agentlab_sandbox_transitions_total`, `agentlab_job_status_total`, and
`agentlab_workspace_snapshot_total`. All metrics share the `agentlab` namespace
under the `sandbox`, `job`, and `workspace` subsystems. In Prometheus, check
that `up{job="agentlab"} == 1`.
