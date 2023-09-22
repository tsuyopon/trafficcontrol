package client

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

import (
	"net/url"
	"strconv"

	"github.com/apache/trafficcontrol/lib/go-tc"
	"github.com/apache/trafficcontrol/traffic_ops/toclientlib"
)

// apiDeliveryServicesRequiredCapabilities is the API version-relative
// route to the /deliveryservices_required_capabilities endpoint.
const apiDeliveryServicesRequiredCapabilities = "/deliveryservices_required_capabilities"

// CreateDeliveryServicesRequiredCapability assigns a Required Capability to a Delivery Service.
func (to *Session) CreateDeliveryServicesRequiredCapability(capability tc.DeliveryServicesRequiredCapability, opts RequestOptions) (tc.Alerts, toclientlib.ReqInf, error) {
	var alerts tc.Alerts
	// /api/4.0/deliveryservices_required_capabilities (POST)
	// see: https://traffic-control-cdn.readthedocs.io/en/v7.0.1/api/v4/deliveryservices_required_capabilities.html#post
	reqInf, err := to.post(apiDeliveryServicesRequiredCapabilities, opts, capability, &alerts)
	return alerts, reqInf, err
}

// DeleteDeliveryServicesRequiredCapability unassigns a Required Capability from a Delivery Service.
func (to *Session) DeleteDeliveryServicesRequiredCapability(deliveryserviceID int, capability string, opts RequestOptions) (tc.Alerts, toclientlib.ReqInf, error) {

	var alerts tc.Alerts
	if opts.QueryParameters == nil {
		opts.QueryParameters = url.Values{}
	}

	// DELETEメソッドへのクエリパラメターとして下記の2つを指定する
	opts.QueryParameters.Set("deliveryServiceID", strconv.Itoa(deliveryserviceID))
	opts.QueryParameters.Set("requiredCapability", capability)

	// /api/4.0/deliveryservices_required_capabilities (DELETE)
	// see: https://traffic-control-cdn.readthedocs.io/en/v7.0.1/api/v4/deliveryservices_required_capabilities.html#delete
	reqInf, err := to.del(apiDeliveryServicesRequiredCapabilities, opts, &alerts)
	return alerts, reqInf, err
}

// GetDeliveryServicesRequiredCapabilities retrieves a list of relationships
// between Delivery Services and the Capabilities they require.
func (to *Session) GetDeliveryServicesRequiredCapabilities(opts RequestOptions) (tc.DeliveryServicesRequiredCapabilitiesResponse, toclientlib.ReqInf, error) {
	var resp tc.DeliveryServicesRequiredCapabilitiesResponse
	// /api/4.0/deliveryservices_required_capabilities (GET)
	// see: https://traffic-control-cdn.readthedocs.io/en/v7.0.1/api/v4/deliveryservices_required_capabilities.html#get
	reqInf, err := to.get(apiDeliveryServicesRequiredCapabilities, opts, &resp)
	return resp, reqInf, err
}
