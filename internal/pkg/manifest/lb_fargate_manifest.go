// Copyright 2019 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"text/template"

	"github.com/aws/amazon-ecs-cli-v2/templates"
)

// LBFargateManifest holds the configuration to build a container image with an exposed port that receives
// requests through a load balancer with AWS Fargate as the compute engine.
type LBFargateManifest struct {
	AppManifest     `yaml:",inline,omitempty"`
	Image           ImageWithPort `yaml:",omitempty"`
	LBFargateConfig `yaml:",inline,omitempty"`
	Environments    map[string]LBFargateConfig `yaml:",omitempty"` // Fields to override per environment.
}

// ImageWithPort represents a container image with an exposed port.
type ImageWithPort struct {
	AppImage `yaml:",inline,omitempty"`
	Port     int `yaml:"port,omitempty"`
}

// LBFargateConfig represents a load balanced web application with AWS Fargate as compute.
type LBFargateConfig struct {
	RoutingRule      `yaml:"http,omitempty"`
	HealthCheck      HealthCheck        `yaml:"healthcheck,omitempty"`
	ContainersConfig `yaml:",inline,omitempty"`
	Database         *DatabaseConfig    `yaml:",omitempty"`
	Scaling          *AutoScalingConfig `yaml:",omitempty"`
}

// ContainersConfig represents the resource boundaries and environment variables for the containers in the service.
type ContainersConfig struct {
	CPU       int               `yaml:"cpu,omitempty"`
	Memory    int               `yaml:"memory,omitempty"`
	Count     int               `yaml:"count,omitempty"`
	Variables map[string]string `yaml:"variables,omitempty"`
	Secrets   map[string]string `yaml:"secrets,omitempty"`
}

// DatabaseConfig represents the resource boundaries for the database in the service.
type DatabaseConfig struct {
	Engine string `yaml:"engine,omitempty"`

	MinCapacity int `yaml:"minCapacity,omitempty"`
	MaxCapacity int `yaml:"maxCapacity,omitempty"`
}

// HealthCheck holds the health check info for the service.
type HealthCheck struct {
	Path string `yaml:"path,omitempty"`
}

// RoutingRule holds the path to route requests to the service.
type RoutingRule struct {
	Path string `yaml:"path,omitempty"`
}

// AutoScalingConfig is the configuration to scale the service with target tracking scaling policies.
type AutoScalingConfig struct {
	MinCount int `yaml:"minCount,omitempty"`
	MaxCount int `yaml:"maxCount,omitempty"`

	TargetCPU    float64 `yaml:"targetCPU,omitempty"`
	TargetMemory float64 `yaml:"targetMemory,omitempty"`
}

// NewLoadBalancedFargateManifest creates a new public load balanced web service with an exposed port of 80, receives
// all the requests from the load balancer and has a single task with minimal CPU and Memory thresholds.
func NewLoadBalancedFargateManifest(appName, dockerfile string, port int) *LBFargateManifest {
	return &LBFargateManifest{
		AppManifest: AppManifest{
			Name: appName,
			Type: LoadBalancedWebApplication,
		},
		Image: ImageWithPort{
			AppImage: AppImage{
				Build: dockerfile,
			},
			Port: port,
		},
		LBFargateConfig: LBFargateConfig{
			RoutingRule: RoutingRule{
				Path: "*",
			},
			HealthCheck: HealthCheck{
				Path: "/",
			},
			ContainersConfig: ContainersConfig{
				CPU:    512,
				Memory: 1024,
				Count:  1,
			},
		},
	}
}

// Marshal serializes the manifest object into a YAML document.
func (m *LBFargateManifest) Marshal() ([]byte, error) {
	box := templates.Box()
	content, err := box.FindString("lb-fargate-service/manifest.yml")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("template").Parse(content)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, *m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DockerfilePath returns the image build path.
func (m LBFargateManifest) DockerfilePath() string {
	return m.Image.Build
}

// EnvConf returns the application configuration with environment overrides.
// If the environment passed in does not have any overrides then we return the default values.
func (m *LBFargateManifest) EnvConf(envName string) LBFargateConfig {
	if _, ok := m.Environments[envName]; !ok {
		return m.LBFargateConfig
	}

	// We don't want to modify the default settings, so deep copy into a "conf" variable.
	envVars := make(map[string]string, len(m.Variables))
	for k, v := range m.Variables {
		envVars[k] = v
	}
	secrets := make(map[string]string, len(m.Secrets))
	for k, v := range m.Secrets {
		secrets[k] = v
	}
	var scaling *AutoScalingConfig
	if m.Scaling != nil {
		scaling = &AutoScalingConfig{
			MinCount:     m.Scaling.MinCount,
			MaxCount:     m.Scaling.MaxCount,
			TargetCPU:    m.Scaling.TargetCPU,
			TargetMemory: m.Scaling.TargetMemory,
		}
	}
	var database *DatabaseConfig
	if m.Database != nil {
		database = &DatabaseConfig{
			Engine:      m.Database.Engine,
			MinCapacity: m.Database.MinCapacity,
			MaxCapacity: m.Database.MaxCapacity,
		}
	}
	conf := LBFargateConfig{
		RoutingRule: RoutingRule{
			Path: m.Path,
		},
		HealthCheck: HealthCheck{
			Path: m.HealthCheck.Path,
		},
		ContainersConfig: ContainersConfig{
			CPU:       m.CPU,
			Memory:    m.Memory,
			Count:     m.Count,
			Variables: envVars,
			Secrets:   secrets,
		},
		Database: database,
		Scaling:  scaling,
	}

	// Override with fields set in the environment.
	target := m.Environments[envName]
	if target.RoutingRule.Path != "" {
		conf.RoutingRule.Path = target.RoutingRule.Path
	}
	if target.HealthCheck.Path != "" {
		conf.HealthCheck.Path = target.HealthCheck.Path
	}
	if target.CPU != 0 {
		conf.CPU = target.CPU
	}
	if target.Memory != 0 {
		conf.Memory = target.Memory
	}
	if target.Count != 0 {
		conf.Count = target.Count
	}
	for k, v := range target.Variables {
		conf.Variables[k] = v
	}
	for k, v := range target.Secrets {
		conf.Secrets[k] = v
	}
	if target.Scaling != nil {
		if conf.Scaling == nil {
			conf.Scaling = &AutoScalingConfig{}
		}
		if target.Scaling.MinCount != 0 {
			conf.Scaling.MinCount = target.Scaling.MinCount
		}
		if target.Scaling.MaxCount != 0 {
			conf.Scaling.MaxCount = target.Scaling.MaxCount
		}
		if target.Scaling.TargetCPU != 0 {
			conf.Scaling.TargetCPU = target.Scaling.TargetCPU
		}
		if target.Scaling.TargetMemory != 0 {
			conf.Scaling.TargetMemory = target.Scaling.TargetMemory
		}
	}
	if target.Database != nil {
		if conf.Database == nil {
			conf.Database = &DatabaseConfig{}
		}
		if target.Database.MinCapacity != 0 {
			conf.Database.MinCapacity = target.Database.MinCapacity
		}
		if target.Database.MaxCapacity != 0 {
			conf.Database.MaxCapacity = target.Database.MaxCapacity
		}
		if target.Database.Engine != "" {
			conf.Database.Engine = target.Database.Engine
		}
	}
	return conf
}

// CFNTemplate serializes the manifest object into a CloudFormation template.
func (m *LBFargateManifest) CFNTemplate() (string, error) {
	return "", nil
}
