package previewaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// Admission refuses the two PreviewAccess records that could never be right
// (ADR-108 §2):
//
//   - a second record on a (jobRef, port) another record already serves:
//     409 Conflict, reason PreviewTargetInUse, naming the existing record;
//   - a record with neither an ownerReference nor expiresAt, which nothing
//     would ever end.
//
// Admission can race (two creates checked before either is stored), so the
// reconciler enforces the first rule as well; this is what makes the common
// case an immediate, named refusal instead of a record that is merely never
// Ready.
type Admission struct {
	Reader client.Reader
}

var _ admission.Handler = (*Admission)(nil)

// PathValidate is where the ValidatingWebhookConfiguration sends requests.
const PathValidate = "/validate-compute-nutgraf-in-v1alpha1-previewaccess"

func (a *Admission) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("")
	}
	var rec computev1alpha1.PreviewAccess
	if err := json.Unmarshal(req.Object.Raw, &rec); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if len(rec.OwnerReferences) == 0 && rec.Spec.ExpiresAt == nil {
		return deny(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid,
			"spec.expiresAt is required when the PreviewAccess has no ownerReference: "+
				"nothing else would ever end it")
	}

	var l computev1alpha1.PreviewAccessList
	if err := a.Reader.List(ctx, &l, client.InNamespace(req.Namespace),
		client.MatchingFields{IndexTarget: TargetKey(rec.Spec.JobRef, rec.Spec.Port)}); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	for _, other := range l.Items {
		if other.Name == rec.Name {
			continue
		}
		return deny(http.StatusConflict, ReasonTargetInUse,
			fmt.Sprintf("PreviewAccess %q already serves %s port %d; one record per (jobRef, port)",
				other.Name, rec.Spec.JobRef, rec.Spec.Port))
	}
	return admission.Allowed("")
}

func deny(code int32, reason metav1.StatusReason, msg string) admission.Response {
	return admission.Response{AdmissionResponse: admissionv1.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{Status: metav1.StatusFailure, Code: code, Reason: reason, Message: msg},
	}}
}
