package hetzner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/template"
)

// validImageRegex ensures the image tag or digest contains no malicious shell characters
var validImageRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+)+(@sha256:[a-fA-F0-9]{64}|:[a-zA-Z0-9_.-]+)?$`)

// WorkerJobSpec is the single contract written to /tmp/job/request.json on the VM.
// It matches the top-level fields the ephemeral-worker's worker.sh reads.
// S3 credentials are intentionally NOT persisted here — they are injected into the
// VM environment by the provisioner (ADR-031 adjustment 4).
type WorkerJobSpec struct {
	JobID          string                 `json:"jobId"`
	Image          string                 `json:"image"`
	Command        []string               `json:"command,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Env            map[string]string      `json:"env,omitempty"`
	Volumes        []VolumeMount          `json:"volumes,omitempty"`
	Input          map[string]interface{} `json:"input,omitempty"`
	Output         OutputSpec             `json:"output,omitempty"`
	CallbackURL    string                 `json:"callbackUrl,omitempty"`
	S3EndpointURL  string                 `json:"s3EndpointUrl,omitempty"`
	S3BucketName   string                 `json:"s3BucketName,omitempty"`
	TimeoutSeconds int                    `json:"timeoutSeconds"`
	LogTailLines   int                    `json:"logTailLines,omitempty"`
}

// VolumeMount defines storage volume mounts for the job container.
type VolumeMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Readonly  bool   `json:"readonly"`
}

// OutputSpec defines output destination references.
type OutputSpec struct {
	S3Prefix string `json:"s3_prefix,omitempty"`
}

// JobPayload is the shape of ephemeral_jobs.payload written by the SDK's
// submit-ephemeral-job transition step.
type JobPayload struct {
	Image             *string           `json:"image"`
	Command           []string          `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	ModelsVolume      *bool             `json:"modelsVolume,omitempty"`
	ModelsVolumeKebab *bool             `json:"models-volume,omitempty"`
	Resources         *struct {
		CPU    *int    `json:"cpu"`
		Memory *string `json:"memory"`
	} `json:"resources,omitempty"`
	Storage *struct {
		ModelVolume    *string `json:"modelVolume"`
		ModelMountPath *string `json:"modelMountPath"`
	} `json:"storage,omitempty"`
	Input  map[string]interface{} `json:"input,omitempty"`
	Output *struct {
		Object string `json:"output,omitempty"`
	} `json:"output,omitempty"`
	CallbackURL    *string `json:"callbackUrl"`
	TimeoutSeconds *int    `json:"timeoutSeconds"`
	ServerType     *string `json:"serverType"`
}

// RequiresModelVolume checks if the job explicitly or implicitly needs a model volume.
func RequiresModelVolume(payload JobPayload) bool {
	if payload.ModelsVolume != nil && !*payload.ModelsVolume {
		return false
	}
	if payload.ModelsVolumeKebab != nil && !*payload.ModelsVolumeKebab {
		return false
	}
	if payload.Storage != nil && payload.Storage.ModelVolume != nil {
		mv := strings.ToLower(strings.TrimSpace(*payload.Storage.ModelVolume))
		if mv == "false" || mv == "none" || mv == "disabled" || mv == "0" {
			return false
		}
		if mv != "" {
			return true
		}
	}
	if payload.Image != nil {
		img := *payload.Image
		if strings.Contains(img, "tts-worker") || strings.Contains(img, "whisper-worker") {
			return true
		}
	}
	return false
}

// CloudInitData holds template data for cloud-init
type CloudInitData struct {
	JobID            string
	RequestJSON      string
	WorkerImage      string
	Command          string
	VolumeDevice     string
	MountPath        string
	CallbackURL      string
	S3EndpointURL    string
	S3BucketName     string
	S3AccessKeyID    string
	S3SecretKey      string
	TailscaleAuthKey string
	GHCRUsername     string
	GHCRToken        string
}

// GenerateCloudInit builds the WorkerJobSpec from the DB payload and renders the
// cloud-init user-data that bootstraps a worker VM for a single ephemeral job.
func GenerateCloudInit(payload JobPayload, jobID string) (string, error) {
	workerImage := os.Getenv("EPHEMERAL_WORKER_IMAGE")
	if payload.Image != nil && *payload.Image != "" {
		workerImage = *payload.Image
	}

	if workerImage == "" {
		return "", fmt.Errorf("EPHEMERAL_WORKER_IMAGE environment variable is required")
	}

	if !validImageRegex.MatchString(workerImage) {
		return "", fmt.Errorf("invalid or unsafe worker image: %q", workerImage)
	}

	mountPath := "/models"
	if payload.Storage != nil && payload.Storage.ModelMountPath != nil && *payload.Storage.ModelMountPath != "" {
		mountPath = *payload.Storage.ModelMountPath
	}

	volumeDevice := ""
	if RequiresModelVolume(payload) {
		volumeID := os.Getenv("VOLUME_ID")
		if payload.Storage != nil && payload.Storage.ModelVolume != nil && *payload.Storage.ModelVolume != "" {
			volumeID = *payload.Storage.ModelVolume
		}
		if volumeID != "" {
			volumeDevice = "/dev/disk/by-id/scsi-0HC_Volume_" + volumeID
		}
	}

	callbackURL := cleanEnv(os.Getenv("CALLBACK_BFF_BASE_URL"))
	if payload.CallbackURL != nil && *payload.CallbackURL != "" {
		callbackURL = cleanEnv(*payload.CallbackURL)
	}

	timeoutSec := 1200
	if payload.TimeoutSeconds != nil && *payload.TimeoutSeconds > 0 {
		timeoutSec = *payload.TimeoutSeconds
	}

	s3AccessKey := cleanEnv(os.Getenv("S3_ACCESS_KEY_ID"))
	s3SecretKey := cleanEnv(os.Getenv("S3_SECRET_ACCESS_KEY"))
	s3EndpointURL := cleanEnv(os.Getenv("S3_ENDPOINT_URL"))
	s3BucketName := cleanEnv(os.Getenv("S3_BUCKET_NAME"))

	spec := WorkerJobSpec{
		JobID:          jobID,
		Image:          workerImage,
		Command:        payload.Command,
		Args:           payload.Args,
		Env:            payload.Env,
		CallbackURL:    callbackURL,
		S3EndpointURL:  s3EndpointURL,
		S3BucketName:   s3BucketName,
		TimeoutSeconds: timeoutSec,
	}

	if payload.Input != nil {
		spec.Input = payload.Input
	}
	if payload.Output != nil && payload.Output.Object != "" {
		spec.Output.S3Prefix = payload.Output.Object
	}

	if volumeDevice != "" && mountPath != "" {
		spec.Volumes = append(spec.Volumes, VolumeMount{
			Host:      mountPath,
			Container: mountPath,
			Readonly:  true,
		})
	}

	rawJSON, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal worker job spec: %w", err)
	}

	indentedJSON := "      " + string(rawJSON)

	tsAuthKey := cleanEnv(os.Getenv("TAILSCALE_AUTHKEY"))
	ghcrUsername := cleanEnv(os.Getenv("GHCR_USERNAME"))
	if ghcrUsername == "" {
		ghcrUsername = "arun4infra"
	}
	ghcrToken := cleanEnv(os.Getenv("GHCR_TOKEN"))

	commandStr := ""
	if len(payload.Command) > 0 {
		commandStr = strings.Join(payload.Command, " ")
		if len(payload.Args) > 0 {
			commandStr += " " + strings.Join(payload.Args, " ")
		}
	} else if strings.Contains(workerImage, "remotion") {
		commandStr = "node scripts/worker-entrypoint.js"
	} else {
		commandStr = "python -u /app/worker-entrypoint.py"
	}

	data := CloudInitData{
		JobID:            jobID,
		RequestJSON:      indentedJSON,
		WorkerImage:      workerImage,
		Command:          commandStr,
		VolumeDevice:     volumeDevice,
		MountPath:        mountPath,
		CallbackURL:      callbackURL,
		S3EndpointURL:    s3EndpointURL,
		S3BucketName:     s3BucketName,
		S3AccessKeyID:    s3AccessKey,
		S3SecretKey:      s3SecretKey,
		TailscaleAuthKey: tsAuthKey,
		GHCRUsername:     ghcrUsername,
		GHCRToken:        ghcrToken,
	}

	tmpl, err := template.New("cloud-init").Parse(cloudInitTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse cloud-init template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute cloud-init template: %w", err)
	}

	cloudInitStr := buf.String()
	if len(cloudInitStr) > 32768 {
		return "", fmt.Errorf("generated cloud-init size (%d bytes) exceeds Hetzner 32KB user_data limit (32768 bytes)", len(cloudInitStr))
	}

	return cloudInitStr, nil
}

const cloudInitTemplate = `#cloud-config
package_update: true
packages:
  - docker.io
  - curl

write_files:
  - path: /tmp/job/request.json
    permissions: '0666'
    content: |
{{.RequestJSON}}
  - path: /tmp/job/run-job.sh
    permissions: '0755'
    content: |
      #!/bin/bash
      set -o pipefail

      chmod -R 777 /tmp/job

      DOCKER_ARGS=()
      DOCKER_ARGS+=("-v" "/tmp/job:/tmp/job")
      DOCKER_ARGS+=("-v" "/var/run/docker.sock:/var/run/docker.sock")
      DOCKER_ARGS+=("--shm-size=2g")
{{if .VolumeDevice}}      DOCKER_ARGS+=("-v" "{{.MountPath}}:{{.MountPath}}")
{{end}}{{if .S3EndpointURL}}      DOCKER_ARGS+=("-e" "S3_ENDPOINT_URL={{.S3EndpointURL}}")
{{end}}{{if .S3BucketName}}      DOCKER_ARGS+=("-e" "S3_BUCKET_NAME={{.S3BucketName}}")
{{end}}{{if .S3AccessKeyID}}      DOCKER_ARGS+=("-e" "S3_ACCESS_KEY_ID={{.S3AccessKeyID}}")
{{end}}{{if .S3SecretKey}}      DOCKER_ARGS+=("-e" "S3_SECRET_ACCESS_KEY={{.S3SecretKey}}")
{{end}}
      EXIT_CODE=0
      docker run --rm --name job-{{.JobID}} "${DOCKER_ARGS[@]}" {{.WorkerImage}}{{if .Command}} {{.Command}}{{end}} || EXIT_CODE=$?

      chmod -R 777 /tmp/job

      echo "[cloudinit-runner] Container finished with exit code ${EXIT_CODE}"

      CALLBACK_URL=""
      if [ -f /tmp/job/request.json ]; then
        CALLBACK_URL=$(grep -o '"callbackUrl": *"[^"]*"' /tmp/job/request.json | cut -d'"' -f4 || true)
      fi
{{if .CallbackURL}}      if [ -z "$CALLBACK_URL" ]; then
        CALLBACK_URL="{{.CallbackURL}}"
      fi
{{end}}
      if [ -n "$CALLBACK_URL" ]; then
        PAYLOAD=""
        if [ -f /tmp/job/result.json ]; then
          RESULT_CONTENT=$(cat /tmp/job/result.json)
          TOKEN=$(echo "$CALLBACK_URL" | grep -o 'token=[^&]*' | cut -d= -f2 || true)
          if [ -n "$TOKEN" ]; then
            PAYLOAD="{\"token\":\"$TOKEN\",\"answer\":$RESULT_CONTENT}"
          else
            PAYLOAD="$RESULT_CONTENT"
          fi
        else
          TOKEN=$(echo "$CALLBACK_URL" | grep -o 'token=[^&]*' | cut -d= -f2 || true)
          if [ $EXIT_CODE -eq 0 ]; then
            STATUS_JSON="{\"status\":\"COMPLETED\",\"exitCode\":0}"
          else
            STATUS_JSON="{\"status\":\"FAILED\",\"exitCode\":$EXIT_CODE,\"error\":\"Container exited with code $EXIT_CODE\"}"
          fi
          if [ -n "$TOKEN" ]; then
            PAYLOAD="{\"token\":\"$TOKEN\",\"answer\":$STATUS_JSON}"
          else
            PAYLOAD="$STATUS_JSON"
          fi
        fi
        echo "[cloudinit-runner] Sending callback to $CALLBACK_URL"
        curl -s -X POST -H "Content-Type: application/json" -d "$PAYLOAD" "$CALLBACK_URL" || true
      fi

      echo "[cloudinit-runner] Halting VM."
      shutdown -h now

runcmd:
{{if .TailscaleAuthKey}}  - curl -fsSL https://tailscale.com/install.sh | sh
  - tailscale up --authkey={{.TailscaleAuthKey}} --hostname=ephemeral-{{.JobID}} --accept-routes
{{end}}  - systemctl enable docker
  - systemctl start docker
  - mkdir -p {{.MountPath}} /tmp/job
{{if .VolumeDevice}}  - for i in $(seq 1 30); do [ -b {{.VolumeDevice}} ] && break; sleep 1; done
  - blkid {{.VolumeDevice}} >/dev/null 2>&1 || mkfs.ext4 {{.VolumeDevice}} || true
  - mount -o discard,defaults {{.VolumeDevice}} {{.MountPath}} || true
  - echo '{{.VolumeDevice}} {{.MountPath}} ext4 discard,nofail,defaults 0 0' >> /etc/fstab || true
{{end}}{{if .GHCRToken}}  - echo "{{.GHCRToken}}" | docker login ghcr.io -u "{{.GHCRUsername}}" --password-stdin
{{end}}  - docker pull {{.WorkerImage}}
  - /tmp/job/run-job.sh
`

func cleanEnv(val string) string {
	lines := strings.Split(val, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func indentText(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
