//  Copyright 2019 Google Inc. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package ovfimporter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/common/utils/logging"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/gce_ovf_import/ovf_import_params"
	"github.com/GoogleCloudPlatform/compute-image-tools/daisy"
	"github.com/GoogleCloudPlatform/compute-image-tools/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi/ovf"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/iterator"
)

func TestSetUpWorkflowHappyPathFromOVANoExtraFlags(t *testing.T) {
	params := GetAllParams()
	params.Project = ""
	params.Zone = ""
	params.MachineType = ""
	params.ScratchBucketGcsPath = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()

	project := "goog-project"

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().ProjectID().Return(project, nil)
	mockMetadataGce.EXPECT().Zone().Return("europe-north1-b", nil)

	mockStorageClient := mocks.NewMockStorageClientInterface(mockCtrl)
	createdScratchBucketName := "ggoo-project-ovf-import-bkt-europe-north1"
	mockStorageClient.EXPECT().CreateBucket(createdScratchBucketName, project,
		&storage.BucketAttrs{
			Name:     createdScratchBucketName,
			Location: "europe-north1",
		}).Return(nil)

	mockComputeClient := mocks.NewMockClient(mockCtrl)
	mockComputeClient.EXPECT().ListMachineTypes(project, "europe-north1-b").
		Return(machineTypes, nil).Times(1)

	mockOvfDescriptorLoader := mocks.NewMockOvfDescriptorLoaderInterface(mockCtrl)
	mockOvfDescriptorLoader.EXPECT().Load(
		fmt.Sprintf("gs://%v/ovf-import-build123/ovf/", createdScratchBucketName)).Return(
		createOVFDescriptor(), nil)

	mockMockTarGcsExtractorInterface := mocks.NewMockTarGcsExtractorInterface(mockCtrl)
	mockMockTarGcsExtractorInterface.EXPECT().ExtractTarToGcs(
		"gs://ovfbucket/ovfpath/vmware.ova",
		fmt.Sprintf("gs://%v/ovf-import-build123/ovf", createdScratchBucketName)).
		Return(nil).Times(1)

	someBucketAttrs := &storage.BucketAttrs{
		Name:     "some-bucket",
		Location: "us-west2",
	}
	mockBucketIterator := mocks.NewMockBucketIteratorInterface(mockCtrl)
	mockBucketIterator.EXPECT().Next().Return(someBucketAttrs, nil)
	mockBucketIterator.EXPECT().Next().Return(nil, iterator.Done)

	mockBucketIteratorCreator := mocks.NewMockBucketIteratorCreatorInterface(mockCtrl)
	mockBucketIteratorCreator.EXPECT().
		CreateBucketIterator(ctx, mockStorageClient, project).
		Return(mockBucketIterator)

	oi := OVFImporter{mgce: mockMetadataGce, workflowPath: "../../test_data/test_import_ovf.wf.json",
		storageClient: mockStorageClient, computeClient: mockComputeClient, buildID: "build123",
		ovfDescriptorLoader: mockOvfDescriptorLoader, tarGcsExtractor: mockMockTarGcsExtractorInterface,
		ctx: ctx, bucketIteratorCreator: mockBucketIteratorCreator,
		Logger: logging.NewLogger("test"), params: params}
	w, err := oi.setUpImportWorkflow()

	assert.Nil(t, err)
	assert.NotNil(t, w)

	oi.modifyWorkflowPreValidate(w)
	oi.modifyWorkflowPostValidate(w)
	assert.Equal(t, "n1-highcpu-16", w.Vars["machine_type"].Value)
	assert.Equal(t, project, w.Project)
	assert.Equal(t, "europe-north1-b", w.Zone)
	assert.Equal(t, fmt.Sprintf("gs://%v/", createdScratchBucketName), w.GCSPath)
	assert.Equal(t, "oAuthFilePath", w.OAuthPath)
	assert.Equal(t, "3h", w.DefaultTimeout)
	assert.Equal(t, 3+3*3, len(w.Steps))

	instance := (*w.Steps["create-instance"].CreateInstances)[0].Instance
	assert.Equal(t, "build123", instance.Labels["gce-ovf-import-build-id"])
	assert.Equal(t, "uservalue1", instance.Labels["userkey1"])
	assert.Equal(t, "uservalue2", instance.Labels["userkey2"])
	assert.Equal(t, false, *instance.Scheduling.AutomaticRestart)
	assert.Equal(t, 1, len(instance.Scheduling.NodeAffinities))
	assert.Equal(t, "env", instance.Scheduling.NodeAffinities[0].Key)
	assert.Equal(t, "IN", instance.Scheduling.NodeAffinities[0].Operator)
	assert.Equal(t, 2, len(instance.Scheduling.NodeAffinities[0].Values))
	assert.Equal(t, "prod", instance.Scheduling.NodeAffinities[0].Values[0])
	assert.Equal(t, "test", instance.Scheduling.NodeAffinities[0].Values[1])

	assert.Equal(t, fmt.Sprintf("gs://%v/ovf-import-build123/ovf/", createdScratchBucketName),
		oi.gcsPathToClean)
}

func TestSetUpWorkflowHappyPathFromOVAExistingScratchBucketProjectZoneAsFlags(t *testing.T) {
	params := GetAllParams()
	params.Project = "aProject"
	params.Zone = "europe-west2-b"
	params.MachineType = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	mockStorageClient := mocks.NewMockStorageClientInterface(mockCtrl)

	mockComputeClient := mocks.NewMockClient(mockCtrl)
	mockComputeClient.EXPECT().ListMachineTypes("aProject", "europe-west2-b").
		Return(machineTypes, nil).Times(1)

	mockOvfDescriptorLoader := mocks.NewMockOvfDescriptorLoaderInterface(mockCtrl)
	mockOvfDescriptorLoader.EXPECT().Load("gs://bucket/folder/ovf-import-build123/ovf/").Return(
		createOVFDescriptor(), nil)

	mockMockTarGcsExtractorInterface := mocks.NewMockTarGcsExtractorInterface(mockCtrl)
	mockMockTarGcsExtractorInterface.EXPECT().ExtractTarToGcs(
		"gs://ovfbucket/ovfpath/vmware.ova", "gs://bucket/folder/ovf-import-build123/ovf").
		Return(nil).Times(1)

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject", "europe-west2-b").Return(nil)

	oi := OVFImporter{mgce: mockMetadataGce, workflowPath: "../../test_data/test_import_ovf.wf.json",
		storageClient: mockStorageClient, computeClient: mockComputeClient, buildID: "build123",
		ovfDescriptorLoader: mockOvfDescriptorLoader, tarGcsExtractor: mockMockTarGcsExtractorInterface,
		Logger: logging.NewLogger("test"), zoneValidator: mockZoneValidator, params: params}
	w, err := oi.setUpImportWorkflow()

	assert.Nil(t, err)
	assert.NotNil(t, w)

	oi.modifyWorkflowPreValidate(w)
	oi.modifyWorkflowPostValidate(w)
	assert.Equal(t, "n1-highcpu-16", w.Vars["machine_type"].Value)
	assert.Equal(t, "aProject", w.Project)
	assert.Equal(t, "europe-west2-b", w.Zone)
	assert.Equal(t, "gs://bucket/folder", w.GCSPath)
	assert.Equal(t, "oAuthFilePath", w.OAuthPath)
	assert.Equal(t, "3h", w.DefaultTimeout)
	assert.Equal(t, 3+3*3, len(w.Steps))
	assert.Equal(t, "build123", (*w.Steps["create-instance"].CreateInstances)[0].
		Instance.Labels["gce-ovf-import-build-id"])
	assert.Equal(t, "uservalue1", (*w.Steps["create-instance"].CreateInstances)[0].
		Instance.Labels["userkey1"])
	assert.Equal(t, "uservalue2", (*w.Steps["create-instance"].CreateInstances)[0].
		Instance.Labels["userkey2"])
	assert.Equal(t, "gs://bucket/folder/ovf-import-build123/ovf/", oi.gcsPathToClean)
}

func TestSetUpWorkflowPopulateMissingParametersError(t *testing.T) {
	params := GetAllParams()
	params.Project = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	w, err := oi.setUpImportWorkflow()

	assert.NotNil(t, err)
	assert.Nil(t, w)
}

func TestSetUpWorkflowPopulateFlagValidationFailed(t *testing.T) {
	params := GetAllParams()
	params.InstanceNames = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	w, err := oi.setUpImportWorkflow()

	assert.NotNil(t, err)
	assert.Nil(t, w)
}

func TestSetUpWorkflowErrorUnpackingOVA(t *testing.T) {
	params := GetAllParams()
	params.Project = "aProject"
	params.Zone = "europe-north1-b"
	params.MachineType = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()

	mockStorageClient := mocks.NewMockStorageClientInterface(mockCtrl)

	mockMockTarGcsExtractorInterface := mocks.NewMockTarGcsExtractorInterface(mockCtrl)
	mockMockTarGcsExtractorInterface.EXPECT().ExtractTarToGcs(
		"gs://ovfbucket/ovfpath/vmware.ova", "gs://bucket/folder/ovf-import-build123/ovf").
		Return(errors.New("tar error")).Times(1)

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject", "europe-north1-b").Return(nil)

	oi := OVFImporter{mgce: mockMetadataGce, workflowPath: "../../test_data/test_import_ovf.wf.json",
		storageClient: mockStorageClient, buildID: "build123",
		tarGcsExtractor: mockMockTarGcsExtractorInterface, Logger: logging.NewLogger("test"),
		zoneValidator: mockZoneValidator, params: params}
	w, err := oi.setUpImportWorkflow()

	assert.NotNil(t, err)
	assert.Nil(t, w)
}

func TestSetUpWorkflowErrorLoadingDescriptor(t *testing.T) {
	params := GetAllParams()
	params.Project = "aProject"
	params.Zone = "europe-north1-b"
	params.OvfOvaGcsPath = "gs://ovfbucket/ovffolder/"
	params.MachineType = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	mockOvfDescriptorLoader := mocks.NewMockOvfDescriptorLoaderInterface(mockCtrl)
	mockOvfDescriptorLoader.EXPECT().Load("gs://ovfbucket/ovffolder/").Return(
		nil, errors.New("ovf desc error"))

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject", "europe-north1-b").Return(nil)

	oi := OVFImporter{mgce: mockMetadataGce, workflowPath: "../../test_data/test_import_ovf.wf.json",
		buildID: "build123", ovfDescriptorLoader: mockOvfDescriptorLoader,
		Logger: logging.NewLogger("test"), zoneValidator: mockZoneValidator, params: params}
	w, err := oi.setUpImportWorkflow()

	assert.NotNil(t, err)
	assert.Nil(t, w)
	assert.Equal(t, "", oi.gcsPathToClean)
}

func TestSetUpWorkOSIdFromOVFDescriptor(t *testing.T) {
	params := GetAllParams()
	params.OsID = ""
	params.OvfOvaGcsPath = "gs://ovfbucket/ovffolder/"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w, err := setupMocksForOSIdTesting(mockCtrl, "rhel7_64Guest", params)

	assert.Nil(t, err)
	assert.NotNil(t, w)
	assert.Equal(t, "../image_import/enterprise_linux/translate_rhel_7_licensed.wf.json", w.Vars["translate_workflow"].Value)
}

func TestSetUpWorkOSIdFromDescriptorInvalidAndOSFlagNotSpecified(t *testing.T) {
	params := GetAllParams()
	params.OsID = ""
	params.OvfOvaGcsPath = "gs://ovfbucket/ovffolder/"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w, err := setupMocksForOSIdTesting(mockCtrl, "no-OS-ID", params)

	assert.Nil(t, w)
	assert.NotNil(t, err)
}

func TestSetUpWorkOSIdFromDescriptorNonDeterministicAndOSFlagNotSpecified(t *testing.T) {
	params := GetAllParams()
	params.OsID = ""
	params.OvfOvaGcsPath = "gs://ovfbucket/ovffolder/"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w, err := setupMocksForOSIdTesting(mockCtrl, "ubuntu64Guest", params)

	assert.Nil(t, w)
	assert.NotNil(t, err)
}

func TestSetUpWorkOSFlagInvalid(t *testing.T) {
	params := GetAllParams()
	params.OsID = "not-OS-ID"
	params.OvfOvaGcsPath = "gs://ovfbucket/ovffolder/"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	w, err := setupMocksForOSIdTesting(mockCtrl, "", params)

	assert.Nil(t, w)
	assert.NotNil(t, err)
}

func setupMocksForOSIdTesting(mockCtrl *gomock.Controller, osType string,
	params *ovfimportparams.OVFImportParams) (*daisy.Workflow, error) {
	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	mockStorageClient := mocks.NewMockStorageClientInterface(mockCtrl)
	mockOvfDescriptorLoader := mocks.NewMockOvfDescriptorLoaderInterface(mockCtrl)

	descriptor := createOVFDescriptor()
	if osType != "" {
		descriptor.VirtualSystem.OperatingSystem = []ovf.OperatingSystemSection{{OSType: &osType}}
	}
	mockOvfDescriptorLoader.EXPECT().Load("gs://ovfbucket/ovffolder/").Return(
		descriptor, nil)

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject", "us-central1-c").Return(nil)

	oi := OVFImporter{mgce: mockMetadataGce, workflowPath: "../../test_data/test_import_ovf.wf.json",
		storageClient: mockStorageClient, buildID: "build123",
		ovfDescriptorLoader: mockOvfDescriptorLoader, Logger: logging.NewLogger("test"),
		zoneValidator: mockZoneValidator, params: params}
	return oi.setUpImportWorkflow()
}

func TestCleanUp(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockStorageClient := mocks.NewMockStorageClientInterface(mockCtrl)
	mockStorageClient.EXPECT().DeleteGcsPath("aPath")
	mockStorageClient.EXPECT().Close()

	oi := OVFImporter{storageClient: mockStorageClient, gcsPathToClean: "aPath",
		Logger: logging.NewLogger("test")}
	oi.CleanUp()
}

func TestBuildDaisyVarsFromDisk(t *testing.T) {
	oi := OVFImporter{params: GetAllParams()}
	varMap := oi.buildDaisyVars("translateworkflow.wf.json", "gs://abucket/apath/bootdisk.vmdk", "n1-standard-2", "aRegion")

	assert.Equal(t, "instance1", varMap["instance_name"])
	assert.Equal(t, "translateworkflow.wf.json", varMap["translate_workflow"])
	assert.Equal(t, strconv.FormatBool(false), varMap["install_gce_packages"])
	assert.Equal(t, "gs://abucket/apath/bootdisk.vmdk", varMap["boot_disk_file"])
	assert.Equal(t, "global/networks/aNetwork", varMap["network"])
	assert.Equal(t, "regions/aRegion/subnetworks/aSubnet", varMap["subnet"])
	assert.Equal(t, "n1-standard-2", varMap["machine_type"])
	assert.Equal(t, "aDescription", varMap["description"])
	assert.Equal(t, "10.0.0.1", varMap["private_network_ip"])
	assert.Equal(t, "PREMIUM", varMap["network_tier"])

	assert.Equal(t, len(varMap), 10)
}

func TestProjectFromGCE(t *testing.T) {
	params := GetAllParams()
	params.Project = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().ProjectID().Return("aProject", nil)

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	project, err := oi.getProject()

	assert.Nil(t, err)
	assert.Equal(t, "aProject", project)
}

func TestGetZoneFromGCE(t *testing.T) {
	params := GetAllParams()
	params.Zone = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().Zone().Return("europe-north1-b", nil)

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	zone, err := oi.getZone("aProject")

	assert.Nil(t, err)
	assert.Equal(t, "europe-north1-b", zone)
}

func TestGetRegionE(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	oi := OVFImporter{Logger: logging.NewLogger("test")}
	region, err := oi.getRegion("europe-north1-b")

	assert.Nil(t, err)
	assert.Equal(t, "europe-north1", region)
}

func TestGetProjectFromFlagEvenIfOnGCE(t *testing.T) {
	params := GetAllParams()
	params.Project = "aProject123"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().ProjectID().Return("aProject", nil).AnyTimes()

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	project, err := oi.getProject()

	assert.Nil(t, err)
	assert.Equal(t, "aProject123", project)
}

func TestGetZoneFromFlagEvenIfOnGCE(t *testing.T) {
	params := GetAllParams()
	params.Zone = "aZone123"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().Zone().Return("europe-north1-b", nil).AnyTimes()

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject123", "aZone123").Return(nil)

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"),
		zoneValidator: mockZoneValidator, params: params}
	zone, err := oi.getZone("aProject123")

	assert.Nil(t, err)
	assert.Equal(t, "aZone123", zone)
}

func TestPopulateMissingParametersProjectEmptyNotOnGCE(t *testing.T) {
	params := GetAllParams()
	params.Project = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	project, err := oi.getProject()
	assert.NotNil(t, err)
	assert.Equal(t, "", project)
}

func TestPopulateMissingParametersErrorRetrievingProjectIDFromGCE(t *testing.T) {
	params := GetAllParams()
	params.Project = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().ProjectID().Return("", errors.New("err"))

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	project, err := oi.getProject()
	assert.NotNil(t, err)
	assert.Equal(t, "", project)
}

func TestGetZoneWhenZoneFlagNotSetNotOnGCE(t *testing.T) {
	params := GetAllParams()
	params.Zone = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(false).AnyTimes()

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	zone, err := oi.getZone("aProject")

	assert.NotNil(t, err)
	assert.Equal(t, "", zone)
}

func TestGetZoneErrorRetrievingZone(t *testing.T) {
	params := GetAllParams()
	params.Zone = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().Zone().Return("", errors.New("err"))

	oi := OVFImporter{mgce: mockMetadataGce, params: params}
	zone, err := oi.getZone("aProject")

	assert.NotNil(t, err)
	assert.Equal(t, "", zone)
}

func TestGetZoneEmptyZoneReturnedFromGCE(t *testing.T) {
	params := GetAllParams()
	params.Zone = ""

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockMetadataGce := mocks.NewMockMetadataGCEInterface(mockCtrl)
	mockMetadataGce.EXPECT().OnGCE().Return(true).AnyTimes()
	mockMetadataGce.EXPECT().Zone().Return("", nil)

	oi := OVFImporter{mgce: mockMetadataGce, Logger: logging.NewLogger("test"), params: params}
	zone, err := oi.getZone("aProject")

	assert.NotNil(t, err)
	assert.Equal(t, "", zone)
}

func TestPopulateMissingParametersInvalidZone(t *testing.T) {
	params := GetAllParams()
	params.Zone = "europe-north1-b"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockZoneValidator := mocks.NewMockZoneValidatorInterface(mockCtrl)
	mockZoneValidator.EXPECT().
		ZoneValid("aProject", "europe-north1-b").Return(fmt.Errorf("error"))

	oi := OVFImporter{Logger: logging.NewLogger("test"), zoneValidator: mockZoneValidator,
		params: params}
	_, err := oi.getZone("aProject")

	assert.NotNil(t, err)
	assert.Equal(t, "europe-north1-b", params.Zone)
}

func createControllerItem(instanceID string, resourceType uint16) ovf.ResourceAllocationSettingData {
	return ovf.ResourceAllocationSettingData{
		CIMResourceAllocationSettingData: ovf.CIMResourceAllocationSettingData{
			InstanceID:   instanceID,
			ResourceType: &resourceType,
		},
	}
}

func createDiskItem(instanceID string, addressOnParent string,
	elementName string, hostResource string, parent string) ovf.ResourceAllocationSettingData {
	diskType := uint16(17)
	return ovf.ResourceAllocationSettingData{
		CIMResourceAllocationSettingData: ovf.CIMResourceAllocationSettingData{
			InstanceID:      instanceID,
			ResourceType:    &diskType,
			AddressOnParent: &addressOnParent,
			ElementName:     elementName,
			HostResource:    []string{hostResource},
			Parent:          &parent,
		},
	}
}

func createOVFDescriptor() *ovf.Envelope {
	virtualHardware := ovf.VirtualHardwareSection{
		Item: []ovf.ResourceAllocationSettingData{
			createControllerItem("5", 6),
			createDiskItem("7", "1", "disk1",
				"ovf:/disk/vmdisk2", "5"),
			createDiskItem("6", "0", "disk0",
				"ovf:/disk/vmdisk1", "5"),
			createDiskItem("8", "2", "disk2",
				"ovf:/disk/vmdisk3", "5"),
			createCPUItem("11", 16),
			createMemoryItem("12", 4096),
		},
	}
	diskCapacityAllocationUnits := "byte * 2^30"
	fileRef1 := "file1"
	fileRef2 := "file2"
	fileRef3 := "file3"
	ovfDescriptor := &ovf.Envelope{
		Disk: &ovf.DiskSection{Disks: []ovf.VirtualDiskDesc{
			{Capacity: "20", CapacityAllocationUnits: &diskCapacityAllocationUnits, DiskID: "vmdisk1", FileRef: &fileRef1},
			{Capacity: "1", CapacityAllocationUnits: &diskCapacityAllocationUnits, DiskID: "vmdisk2", FileRef: &fileRef2},
			{Capacity: "5", CapacityAllocationUnits: &diskCapacityAllocationUnits, DiskID: "vmdisk3", FileRef: &fileRef3},
		}},
		References: []ovf.File{
			{Href: "Ubuntu_for_Horizon71_1_1.0-disk1.vmdk", ID: "file1", Size: 1151322112},
			{Href: "Ubuntu_for_Horizon71_1_1.0-disk2.vmdk", ID: "file2", Size: 68096},
			{Href: "Ubuntu_for_Horizon71_1_1.0-disk3.vmdk", ID: "file3", Size: 68096},
		},
		VirtualSystem: &ovf.VirtualSystem{
			VirtualHardware: []ovf.VirtualHardwareSection{virtualHardware},
		},
	}
	return ovfDescriptor
}

func createCPUItem(instanceID string, quantity uint) ovf.ResourceAllocationSettingData {
	resourceType := uint16(3)
	mhz := "hertz * 10^6"
	return ovf.ResourceAllocationSettingData{
		CIMResourceAllocationSettingData: ovf.CIMResourceAllocationSettingData{
			InstanceID:      instanceID,
			ResourceType:    &resourceType,
			VirtualQuantity: &quantity,
			AllocationUnits: &mhz,
		},
	}
}

func createMemoryItem(instanceID string, quantityMB uint) ovf.ResourceAllocationSettingData {
	resourceType := uint16(4)
	mb := "byte * 2^20"

	return ovf.ResourceAllocationSettingData{
		CIMResourceAllocationSettingData: ovf.CIMResourceAllocationSettingData{
			InstanceID:      instanceID,
			ResourceType:    &resourceType,
			VirtualQuantity: &quantityMB,
			AllocationUnits: &mb,
		},
	}
}

func GetAllParams() *ovfimportparams.OVFImportParams {
	return &ovfimportparams.OVFImportParams{
		InstanceNames:               "instance1",
		ClientID:                    "aClient",
		OvfOvaGcsPath:               "gs://ovfbucket/ovfpath/vmware.ova",
		NoGuestEnvironment:          true,
		CanIPForward:                true,
		DeletionProtection:          true,
		Description:                 "aDescription",
		Labels:                      "userkey1=uservalue1,userkey2=uservalue2",
		MachineType:                 "n1-standard-2",
		Network:                     "aNetwork",
		Subnet:                      "aSubnet",
		NetworkTier:                 "PREMIUM",
		PrivateNetworkIP:            "10.0.0.1",
		NoExternalIP:                true,
		NoRestartOnFailure:          true,
		OsID:                        "ubuntu-1404",
		ShieldedIntegrityMonitoring: true,
		ShieldedSecureBoot:          true,
		ShieldedVtpm:                true,
		Tags:                        "tag1=val1",
		Zone:                        "us-central1-c",
		BootDiskKmskey:              "aKey",
		BootDiskKmsKeyring:          "aKeyring",
		BootDiskKmsLocation:         "aKmsLocation",
		BootDiskKmsProject:          "aKmsProject",
		Timeout:                     "3h",
		Project:                     "aProject",
		ScratchBucketGcsPath:        "gs://bucket/folder",
		Oauth:                       "oAuthFilePath",
		Ce:                          "us-east1-c",
		GcsLogsDisabled:             true,
		CloudLogsDisabled:           true,
		StdoutLogsDisabled:          true,
		NodeAffinityLabelsFlag:      []string{"env,IN,prod,test"},
	}
}

var machineTypes = []*compute.MachineType{
	{
		GuestCpus:                    1,
		Id:                           2000,
		IsSharedCpu:                  true,
		MaximumPersistentDisks:       16,
		MaximumPersistentDisksSizeGb: 3072,
		MemoryMb:                     1740,
		Name:                         "g1-small",
		Zone:                         "us-east1-b",
	},
	{
		GuestCpus:                    16,
		Id:                           4016,
		ImageSpaceGb:                 10,
		MaximumPersistentDisks:       128,
		MaximumPersistentDisksSizeGb: 65536,
		MemoryMb:                     14746,
		Name:                         "n1-highcpu-16",
		Zone:                         "us-east1-b",
	},
}