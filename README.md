[![Go build](https://github.com/Netcracker/qubership-core-maas-agent/actions/workflows/go-build.yml/badge.svg)](https://github.com/Netcracker/qubership-core-maas-agent/actions/workflows/go-build.yml)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?metric=coverage&project=Netcracker_qubership-core-maas-agent)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-maas-agent)
[![duplicated_lines_density](https://sonarcloud.io/api/project_badges/measure?metric=duplicated_lines_density&project=Netcracker_qubership-core-maas-agent)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-maas-agent)
[![vulnerabilities](https://sonarcloud.io/api/project_badges/measure?metric=vulnerabilities&project=Netcracker_qubership-core-maas-agent)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-maas-agent)
[![bugs](https://sonarcloud.io/api/project_badges/measure?metric=bugs&project=Netcracker_qubership-core-maas-agent)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-maas-agent)
[![code_smells](https://sonarcloud.io/api/project_badges/measure?metric=code_smells&project=Netcracker_qubership-core-maas-agent)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-maas-agent)

# maas-agent

`maas-agent` is just a proxy to MaaS service specified via properties. The main goal of this agent is to transform internal M2M security with 
local IdP to credentials that accepts global MaaS service. To accomplish this, `maas-agent` predeploy script creates dedicated account for this agent in 
global MaaS and stores it to secret. 

Installation parameters:
* `MAAS_ENABLED` - Optional, but if you don't set it true you can't work with MaaS solution and you will get a error messages on any API. See description below.
* `MAAS_INTERNAL_ADDRESS` - Optional, but if you don't set it properly you can't work with MaaS solution, default will be used. See description below.
* `MAAS_AGENT_NAMESPACE_ISOLATION_ENABLED` - Optional. If false, microservice can request for topics by classifier with any namespace ignoring composite rules.

As mentioned before, `maas-agent` is just a proxy to MaaS service. So, if you want to know more about REST API refer to MaaS documentation:  
https://github.com/Netcracker/qubership-maas/blob/main/README.md.  

