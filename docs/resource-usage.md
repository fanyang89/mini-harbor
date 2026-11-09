# Resource usage and Harbor comparison

[Home](../README.md)

## Measured resource usage

A running k3s deployment observed over the 24 hours ending **2026-11-06 11:22 UTC**:

| Metric | Registry + gateway + web/API |
| --- | --- |
| Average memory working set | **52.3 MiB** |
| Highest sampled memory working set | **60.8 MiB** |
| Average CPU usage | **6.1 millicores** (about **0.6% of one CPU core**) |

These figures come from existing monitoring data, with no synthetic load added: summed container working-set memory and summed 5-minute CPU rates, evaluated at 5-minute intervals over 24 hours. The memory summary includes 287 available samples; the sampled peak can miss shorter spikes. Cluster services, ingress, authentication infrastructure, and disk storage are excluded.

This deployment runs a pre-public-release build, not the latest public commit. These historical figures predate the rqlite service and exclude its resource usage. The figures describe that deployment's observed workload, not a minimum resource requirement or a capacity benchmark. Usage varies with traffic, image catalog size, and the number of cache sources.

## Harbor sizing context

**Around 52 MiB observed for Mini Harbor; Harbor documents a 4 GB minimum and recommends 8 GB of memory for deployment.**

| Resource | Mini Harbor: observed 24-hour average | Harbor 2.15: minimum configuration | Harbor 2.15: recommended configuration |
| --- | --- | --- | --- |
| Memory | **52.3 MiB** | **4 GB** | **8 GB** |
| CPU | **0.0061 cores** | **2 CPUs** | **4 CPUs** |

Mini Harbor's column measures application container usage in the deployment described above. Harbor's columns are published deployment resource requirements, **not measured usage**. This gives sizing context, not a like-for-like performance benchmark or a measured percentage saving. No running Harbor deployment was available in the observed cluster for a like-for-like RAM or CPU comparison.

Source: Harbor 2.15 installation prerequisites, accessed 2026-11-06: https://goharbor.io/docs/2.15.0/install-config/installation-prereqs/

## A smaller stack

**4 containers instead of 9 in the default single-node configuration.** This comparison uses Harbor 2.15.0 with bundled PostgreSQL and Redis, without the optional scanner or metrics exporter. Container count is not a RAM or CPU usage ratio; a three-node metadata cluster adds two more rqlite containers.

| Runtime component | Mini Harbor | Harbor |
| --- | --- | --- |
| Metadata storage | rqlite service | PostgreSQL service |
| Redis | Not required | Part of the standard stack |
| Web console | Static files served by the Go backend | Separate portal service |
| Background job service | No separate job service | Jobservice |
| Vulnerability scanning and replication | Not included | Available |

The default Compose stack has four containers: the registry, the cache gateway, the web/API service, and rqlite. The gateway also runs a Distribution cache process for each configured upstream source. Go and Node.js build tools are used during the image build, not at runtime.

Harbor comparison source: https://github.com/goharbor/harbor/blob/v2.15.0/make/photon/prepare/templates/docker_compose/docker-compose.yml.jinja

Choose Mini Harbor for a focused registry with a UI and cache. Choose Harbor when you need features such as project-level RBAC, integrated vulnerability scanning, or replication policies. Mini Harbor is an independent project, not an official Harbor distribution.
