package secrets

import (
	"strings"
	"testing"
)

func TestValidateEncryptionKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid 32 hex characters",
			key:     "8ec32a8fafb27566fccd50da3d789979",
			wantErr: false,
		},
		{
			name:    "valid 32 hex characters with uppercase",
			key:     "8EC32A8FAFB27566FCCD50DA3D789979",
			wantErr: false,
		},
		{
			name:    "too short - 31 characters",
			key:     "8ec32a8fafb27566fccd50da3d78997",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY must be 32 characters, got 31",
		},
		{
			name:    "too long - 33 characters",
			key:     "8ec32a8fafb27566fccd50da3d7899799",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY must be 32 characters, got 33",
		},
		{
			name:    "empty string",
			key:     "",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY must be 32 characters, got 0",
		},
		{
			name:    "invalid hex character",
			key:     "8ec32a8fafb27566fccd50da3d78997g",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY must be valid hex string",
		},
		{
			name:    "all zeros",
			key:     "00000000000000000000000000000000",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY cannot be all zeros",
		},
		{
			name:    "non-hex characters",
			key:     "xyz32a8fafb27566fccd50da3d789979",
			wantErr: true,
			errMsg:  "ENCRYPTION_KEY must be valid hex string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEncryptionKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateEncryptionKey() expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateEncryptionKey() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateEncryptionKey() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestValidateAuthSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid 32 hex characters",
			secret:  "841e8ab0c28196d44e66b9075019cbfc",
			wantErr: false,
		},
		{
			name:    "valid 32 hex characters with uppercase",
			secret:  "841E8AB0C28196D44E66B9075019CBFC",
			wantErr: false,
		},
		{
			name:    "too short - 31 characters",
			secret:  "841e8ab0c28196d44e66b9075019cbf",
			wantErr: true,
			errMsg:  "AUTH_SECRET must be 32 characters, got 31",
		},
		{
			name:    "too long - 33 characters",
			secret:  "841e8ab0c28196d44e66b9075019cbfcc",
			wantErr: true,
			errMsg:  "AUTH_SECRET must be 32 characters, got 33",
		},
		{
			name:    "empty string",
			secret:  "",
			wantErr: true,
			errMsg:  "AUTH_SECRET must be 32 characters, got 0",
		},
		{
			name:    "invalid hex character",
			secret:  "841e8ab0c28196d44e66b9075019cbfg",
			wantErr: true,
			errMsg:  "AUTH_SECRET must be valid hex string",
		},
		{
			name:    "all zeros",
			secret:  "00000000000000000000000000000000",
			wantErr: true,
			errMsg:  "AUTH_SECRET cannot be all zeros",
		},
		{
			name:    "non-hex characters",
			secret:  "xyz18ab0c28196d44e66b9075019cbfc",
			wantErr: true,
			errMsg:  "AUTH_SECRET must be valid hex string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAuthSecret(tt.secret)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateAuthSecret() expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateAuthSecret() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateAuthSecret() unexpected error = %v", err)
				}
			}
		})
	}
}