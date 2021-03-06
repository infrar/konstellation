/*


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

package v1alpha1

import (
	"fmt"

	"github.com/mitchellh/hashstructure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SpecHashAnnotation = "k11n.dev/lsaSpecHash"
)

// LinkedServiceAccountSpec defines the desired state of LinkedServiceAccount
type LinkedServiceAccountSpec struct {
	Targets []string `json:"targets"`
	// +kubebuilder:validation:Optional
	AWS *LinkedServiceAccountAWSSpec `json:"aws,omitempty"`
}

type LinkedServiceAccountAWSSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	PolicyARNs []string `json:"policyArns"`
}

// ConnectedServiceAccountStatus defines the observed state of LinkedServiceAccount
type LinkedServiceAccountStatus struct {
	LinkedTargets []string `json:"linkedTargets,omitempty"` // list of targets that are linked
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// LinkedServiceAccount is the Schema for the linkedserviceaccounts API
type LinkedServiceAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LinkedServiceAccountSpec   `json:"spec,omitempty"`
	Status LinkedServiceAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LinkedServiceAccountList contains a list of LinkedServiceAccount
type LinkedServiceAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LinkedServiceAccount `json:"items"`
}

func (l *LinkedServiceAccount) NeedsReconcile() (bool, error) {
	hashVal, err := hashstructure.Hash(&l.Spec, nil)
	if err != nil {
		return false, err
	}

	if l.Annotations == nil {
		return true, nil
	}
	return l.Annotations[SpecHashAnnotation] != fmt.Sprintf("%d", hashVal), nil
}

func (l *LinkedServiceAccount) UpdateHash() error {
	hashVal, err := hashstructure.Hash(&l.Spec, nil)
	if err != nil {
		return err
	}

	if l.Annotations == nil {
		l.Annotations = map[string]string{}
	}
	l.Annotations[SpecHashAnnotation] = fmt.Sprintf("%d", hashVal)
	return nil
}

func (l *LinkedServiceAccount) GetPolicies() []string {
	if l.Spec.AWS != nil {
		return l.Spec.AWS.PolicyARNs
	}
	return []string{}
}

func init() {
	SchemeBuilder.Register(&LinkedServiceAccount{}, &LinkedServiceAccountList{})
}
