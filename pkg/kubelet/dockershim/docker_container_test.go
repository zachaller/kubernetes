/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dockershim

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	runtimeApi "k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

// A helper to create a basic config.
func makeContainerConfig(sConfig *runtimeApi.PodSandboxConfig, name, image string, attempt uint32, labels, annotations map[string]string) *runtimeApi.ContainerConfig {
	return &runtimeApi.ContainerConfig{
		Metadata: &runtimeApi.ContainerMetadata{
			Name:    &name,
			Attempt: &attempt,
		},
		Image:       &runtimeApi.ImageSpec{Image: &image},
		Labels:      labels,
		Annotations: annotations,
	}
}

// TestListContainers creates several containers and then list them to check
// whether the correct metadatas, states, and labels are returned.
func TestListContainers(t *testing.T) {
	ds, _, _ := newTestDockerService()
	podName, namespace := "foo", "bar"
	containerName, image := "sidecar", "logger"

	configs := []*runtimeApi.ContainerConfig{}
	sConfigs := []*runtimeApi.PodSandboxConfig{}
	for i := 0; i < 3; i++ {
		s := makeSandboxConfig(fmt.Sprintf("%s%d", podName, i),
			fmt.Sprintf("%s%d", namespace, i), fmt.Sprintf("%d", i), 0)
		labels := map[string]string{"abc.xyz": fmt.Sprintf("label%d", i)}
		annotations := map[string]string{"foo.bar.baz": fmt.Sprintf("annotaion%d", i)}
		c := makeContainerConfig(s, fmt.Sprintf("%s%d", containerName, i),
			fmt.Sprintf("%s:v%d", image, i), uint32(i), labels, annotations)
		sConfigs = append(sConfigs, s)
		configs = append(configs, c)
	}

	expected := []*runtimeApi.Container{}
	state := runtimeApi.ContainerState_RUNNING
	var createdAt int64 = 0
	for i := range configs {
		// We don't care about the sandbox id; pass a bogus one.
		sandboxID := fmt.Sprintf("sandboxid%d", i)
		id, err := ds.CreateContainer(sandboxID, configs[i], sConfigs[i])
		assert.NoError(t, err)
		err = ds.StartContainer(id)
		assert.NoError(t, err)

		imageRef := "" // FakeDockerClient doesn't populate ImageRef yet.
		// Prepend to the expected list because ListContainers returns
		// the most recent containers first.
		expected = append([]*runtimeApi.Container{{
			Metadata:     configs[i].Metadata,
			Id:           &id,
			PodSandboxId: &sandboxID,
			State:        &state,
			CreatedAt:    &createdAt,
			Image:        configs[i].Image,
			ImageRef:     &imageRef,
			Labels:       configs[i].Labels,
			Annotations:  configs[i].Annotations,
		}}, expected...)
	}
	containers, err := ds.ListContainers(nil)
	assert.NoError(t, err)
	assert.Len(t, containers, len(expected))
	assert.Equal(t, expected, containers)
}

// TestContainerStatus tests the basic lifecycle operations and verify that
// the status returned reflects the operations performed.
func TestContainerStatus(t *testing.T) {
	ds, fDocker, fClock := newTestDockerService()
	sConfig := makeSandboxConfig("foo", "bar", "1", 0)
	labels := map[string]string{"abc.xyz": "foo"}
	annotations := map[string]string{"foo.bar.baz": "abc"}
	config := makeContainerConfig(sConfig, "pause", "iamimage", 0, labels, annotations)

	var defaultTime time.Time
	dt := defaultTime.Unix()
	ct, st, ft := dt, dt, dt
	state := runtimeApi.ContainerState_CREATED
	// The following variables are not set in FakeDockerClient.
	imageRef := ""
	exitCode := int32(0)
	var reason, message string

	expected := &runtimeApi.ContainerStatus{
		State:       &state,
		CreatedAt:   &ct,
		StartedAt:   &st,
		FinishedAt:  &ft,
		Metadata:    config.Metadata,
		Image:       config.Image,
		ImageRef:    &imageRef,
		ExitCode:    &exitCode,
		Reason:      &reason,
		Message:     &message,
		Mounts:      []*runtimeApi.Mount{},
		Labels:      config.Labels,
		Annotations: config.Annotations,
	}

	// Create the container.
	fClock.SetTime(time.Now().Add(-1 * time.Hour))
	*expected.CreatedAt = fClock.Now().Unix()
	const sandboxId = "sandboxid"
	id, err := ds.CreateContainer(sandboxId, config, sConfig)

	// Check internal labels
	c, err := fDocker.InspectContainer(id)
	assert.NoError(t, err)
	assert.Equal(t, c.Config.Labels[containerTypeLabelKey], containerTypeLabelContainer)
	assert.Equal(t, c.Config.Labels[sandboxIDLabelKey], sandboxId)

	// Set the id manually since we don't know the id until it's created.
	expected.Id = &id
	assert.NoError(t, err)
	status, err := ds.ContainerStatus(id)
	assert.NoError(t, err)
	assert.Equal(t, expected, status)

	// Advance the clock and start the container.
	fClock.SetTime(time.Now())
	*expected.StartedAt = fClock.Now().Unix()
	*expected.State = runtimeApi.ContainerState_RUNNING

	err = ds.StartContainer(id)
	assert.NoError(t, err)
	status, err = ds.ContainerStatus(id)
	assert.Equal(t, expected, status)

	// Advance the clock and stop the container.
	fClock.SetTime(time.Now().Add(1 * time.Hour))
	*expected.FinishedAt = fClock.Now().Unix()
	*expected.State = runtimeApi.ContainerState_EXITED
	*expected.Reason = "Completed"

	err = ds.StopContainer(id, 0)
	assert.NoError(t, err)
	status, err = ds.ContainerStatus(id)
	assert.Equal(t, expected, status)

	// Remove the container.
	err = ds.RemoveContainer(id)
	assert.NoError(t, err)
	status, err = ds.ContainerStatus(id)
	assert.Error(t, err, fmt.Sprintf("status of container: %+v", status))
}
