package jsonrpc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// RPCRequest holds information about a jsonrpc request object.
// See: http://www.jsonrpc.org/specification#request_object
type RPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      uint        `json:"id"`
}

// RPCNotification holds information about a jsonrpc notification object.
// A notification object omits the id field since there will be no server response.
// See: http://www.jsonrpc.org/specification#notification
type RPCNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RPCResponse holds information about a jsonrpc response object.
// If no rpc specific error occured Error field is nil
// See: http://www.jsonrpc.org/specification#response_object
type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      int         `json:"id"`
}

// RPCError holds information about a jsonrpc error object if an rpc error occured.
// See: http://www.jsonrpc.org/specification#error_object
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// RPCClient is the client that sends jsonrpc requests over http.
type RPCClient struct {
	endpoint        string
	httpClient      *http.Client
	basicAuth       string
	customHeaders   map[string]string
	autoIncrementID bool
	nextID          uint
	idMutex         sync.Mutex
}

// NewRPCClient returns a new RPCClient instance with default configuration.
// endpoint is the rpc-service url to which the rpc requests are sent.
func NewRPCClient(endpoint string) *RPCClient {
	return &RPCClient{
		endpoint:        endpoint,
		httpClient:      http.DefaultClient,
		autoIncrementID: true,
		nextID:          0,
		customHeaders:   make(map[string]string),
	}
}

// NewRPCRequestObject creates and returns a raw RPCRequest structure.
// It is mainly used when building batch requests. For single requests use RPCClient.Call()
// RPCRequest struct can also be created directly, but this function sets the ID and the jsonrpc field to the correct values.
func (client *RPCClient) NewRPCRequestObject(method string, params ...interface{}) *RPCRequest {
	client.idMutex.Lock()
	rpcRequest := RPCRequest{
		ID:      client.nextID,
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	if client.autoIncrementID == true {
		client.nextID++
	}
	client.idMutex.Unlock()

	if len(params) == 0 {
		rpcRequest.Params = nil
	}

	return &rpcRequest
}

// NewRPCNotificationObject creates and returns a raw RPCNotification structure.
// It is mainly used when building batch requests. For single notifications use RPCClient.Notification()
func (client *RPCClient) NewRPCNotificationObject(method string, params ...interface{}) *RPCNotification {
	rpcNotification := RPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	if len(params) == 0 {
		rpcNotification.Params = nil
	}

	return &rpcNotification
}

// Call creates and sends an jsonrpc request using http to the rpc-service url that was provided.
// If something went wrong on the network / http level or if json parsing failed it returns an error.
// If something went wrong on the rpc-service / protocol level the Error field of the returned RPCResponse is set
// and contains information about the error.
// If the request was successful the Error field is nil and the Result field of the RPCRespnse struct contains the rpc result.
func (client *RPCClient) Call(method string, params ...interface{}) (*RPCResponse, error) {
	httpRequest, err := client.newRequest(false, method, params...)
	if err != nil {
		return nil, err
	}

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()

	rpcResponse := RPCResponse{}
	decoder := json.NewDecoder(httpResponse.Body)
	decoder.UseNumber()
	err = decoder.Decode(&rpcResponse)
	if err != nil {
		return nil, err
	}

	return &rpcResponse, nil
}

// Notification sends an jsonrpc request to the rpc-service. The difference to Call() is that this call does not expect a response.
// The ID field of the request is omitted.
func (client *RPCClient) Notification(method string, params ...interface{}) error {
	httpRequest, err := client.newRequest(true, method, params...)
	if err != nil {
		return err
	}

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	return nil
}

// Batch sends a jsonrpc batch request to the server.
// The parameter is a list of requests the could be one of RPCRequest and RPCNotification
// The batch requests returns a list of responses.
func (client *RPCClient) Batch(requests ...interface{}) ([]RPCResponse, error) {
	for _, r := range requests {
		switch r := r.(type) {
		default:
			return nil, fmt.Errorf("Invalid parameter: %s", r)
		case *RPCRequest:
		case *RPCNotification:
		}
	}

	httpRequest, err := client.newBatchRequest(requests...)
	if err != nil {
		return nil, err
	}

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()

	rpcResponses := []RPCResponse{}
	decoder := json.NewDecoder(httpResponse.Body)
	decoder.UseNumber()
	err = decoder.Decode(&rpcResponses)
	if err != nil {
		return nil, err
	}

	return rpcResponses, nil
}

// SetAutoIncrementID if set to true, the id field of an rpcjson request will be incremented automatically
func (client *RPCClient) SetAutoIncrementID(flag bool) {
	client.autoIncrementID = flag
}

// SetNextID can be used to set the next id / reset the id.
func (client *RPCClient) SetNextID(id uint) {
	client.idMutex.Lock()
	client.nextID = id
	client.idMutex.Unlock()
}

func (client *RPCClient) incrementID() {
	client.idMutex.Lock()
	client.nextID++
	client.idMutex.Unlock()
}

// SetCustomHeader is used to set a custom header for each rpc request.
// e.g. set Authorization Bearer here.
func (client *RPCClient) SetCustomHeader(key string, value string) {
	client.customHeaders[key] = value
}

// SetBasicAuth is a helper function that sets the header for the given basic authentication credentials
func (client *RPCClient) SetBasicAuth(username string, password string) {
	if username == "" || password == "" {
		client.basicAuth = ""
		return
	}
	auth := username + ":" + password
	client.basicAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

// SetHTTPClient can be used to set a custom http.Client.
// This can be usefull for example if you want to customize the http.Client behaviour (e.g. proxy settings)
func (client *RPCClient) SetHTTPClient(httpClient *http.Client) {
	client.httpClient = httpClient
}

func (client *RPCClient) newRequest(notification bool, method string, params ...interface{}) (*http.Request, error) {

	// TODO: easier way to remove ID from RPCRequest without extra struct
	var rpcRequest interface{}
	if notification {
		rpcNotification := RPCNotification{
			JSONRPC: "2.0",
			Method:  method,
			Params:  params,
		}
		if len(params) == 0 {
			rpcNotification.Params = nil
		}
		rpcRequest = rpcNotification
	} else {
		client.idMutex.Lock()
		request := RPCRequest{
			ID:      client.nextID,
			JSONRPC: "2.0",
			Method:  method,
			Params:  params,
		}
		if client.autoIncrementID == true {
			client.nextID++
		}
		client.idMutex.Unlock()
		if len(params) == 0 {
			request.Params = nil
		}
		rpcRequest = request
	}

	body, err := json.Marshal(rpcRequest)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest("POST", client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for k, v := range client.customHeaders {
		request.Header.Add(k, v)
	}

	if client.basicAuth != "" {
		request.Header.Add("Authorization", client.basicAuth)
	}
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Accept", "application/json")

	return request, nil
}

func (client *RPCClient) newBatchRequest(requests ...interface{}) (*http.Request, error) {

	body, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest("POST", client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for k, v := range client.customHeaders {
		request.Header.Add(k, v)
	}

	if client.basicAuth != "" {
		request.Header.Add("Authorization", client.basicAuth)
	}
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Accept", "application/json")

	return request, nil
}

// UpdateRequestID updates the ID of an RPCRequest structure.
// This is used if a (batch) request is sent several times and the request should get an updated id
func (client *RPCClient) UpdateRequestID(rpcRequest *RPCRequest) {
	client.idMutex.Lock()
	defer client.idMutex.Unlock()
	rpcRequest.ID = client.nextID
	if client.autoIncrementID == true {
		client.nextID++
	}
}

// GetInt tries to convert the rpc response to an int64 and returns it
func (rpcResponse *RPCResponse) GetInt() (int64, error) {
	val, ok := rpcResponse.Result.(json.Number)
	if !ok {
		return 0, fmt.Errorf("could not parse int from %s", rpcResponse.Result)
	}

	i, err := val.Int64()
	if err != nil {
		return 0, err
	}

	return i, nil
}

// GetFloat tries to convert the rpc response to a float64 and returns it
func (rpcResponse *RPCResponse) GetFloat() (float64, error) {
	val, ok := rpcResponse.Result.(json.Number)
	if !ok {
		return 0, fmt.Errorf("could not parse int from %s", rpcResponse.Result)
	}

	f, err := val.Float64()
	if err != nil {
		return 0, err
	}

	return f, nil
}

// GetBool tries to convert the rpc response to a bool and returns it
func (rpcResponse *RPCResponse) GetBool() (bool, error) {
	val, ok := rpcResponse.Result.(bool)
	if !ok {
		return false, fmt.Errorf("could not parse int from %s", rpcResponse.Result)
	}

	return val, nil
}

// GetString tries to convert the rpc response to a string and returns it
func (rpcResponse *RPCResponse) GetString() (string, error) {
	val, ok := rpcResponse.Result.(string)
	if !ok {
		return "", fmt.Errorf("could not parse int from %s", rpcResponse.Result)
	}

	return val, nil
}

// GetObject tries to convert the rpc response to an object (e.g. a struct) and returns it
func (rpcResponse *RPCResponse) GetObject(toStruct interface{}) error {
	js, err := json.Marshal(rpcResponse.Result)
	if err != nil {
		return err
	}

	err = json.Unmarshal(js, toStruct)
	if err != nil {
		return err
	}

	return nil
}
