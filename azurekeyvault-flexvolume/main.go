// Copyright (c) Microsoft and contributors.  All rights reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/golang/glog"
	"github.com/pkg/errors"

	kv "github.com/Azure/azure-sdk-for-go/services/keyvault/2016-10-01/keyvault"
	kvmgmt "github.com/Azure/azure-sdk-for-go/services/keyvault/mgmt/2016-10-01/keyvault"
)

const (
	program                = "azurekeyvault-flexvolume"
	version                = "0.0.6"
	permission os.FileMode = 0644
	objectsSep             = ";"
)

// Type of Azure Key Vault objects
const (
	// VaultTypeSecret secret vault object type
	VaultTypeSecret string = "secret"
	// VaultTypeKey key vault object type
	VaultTypeKey string = "key"
	// VaultTypeCertificate certificate vault object type
	VaultTypeCertificate string = "cert"
)

// Option is a collection of configs
type Option struct {
	// the name of the Azure Key Vault instance
	vaultName string
	// the name of the Azure Key Vault objects
	vaultObjectNames string
	// the versions of the Azure Key Vault objects
	vaultObjectVersions string
	// the types of the Azure Key Vault objects
	vaultObjectTypes string
	// the resourcegroup of the Azure Key Vault
	resourceGroup string
	// directory to save the vault objects
	dir string
	// subscriptionId to azure
	subscriptionId string
	// version flag
	showVersion bool
	// cloud name
	cloudName string
	// tenantID in AAD
	tenantId string
	// POD AAD Identity flag
	usePodIdentity bool
	// AAD app client secret (if not using POD AAD Identity)
	aADClientSecret string
	// AAD app client secret id (if not using POD AAD Identity)
	aADClientID string
	// the name of the pod (if using POD AAD Identity)
	podName string
	// the namespace of the pod (if using POD AAD Identity)
	podNamespace string
}

//KeyvaultFlexvolumeAdapter
type KeyvaultFlexvolumeAdapter struct {
	ctx     context.Context
	options Option
}

var (
	options Option
)

func main() {
	context := context.Background()

	if err := parseConfigs(); err != nil {
		glog.Fatalf("[error] : %s", err)
	}

	adapter := &KeyvaultFlexvolumeAdapter{ctx: context, options: options}
	err := adapter.Run()
	if err != nil {
		glog.Fatalf("[error] : %s", err)
	}
	os.Exit(0)
}

//Run fetches the specified objects from keyvault and writes them on dir
func (adapter *KeyvaultFlexvolumeAdapter) Run() error {
	options := adapter.options
	ctx := adapter.ctx
	if options.showVersion {
		fmt.Printf("%s %s\n", program, version)
		fmt.Printf("%s \n", options.subscriptionId)
	}

	_, err := os.Lstat(options.dir)
	if err != nil {
		return errors.Wrapf(err, "failed to get directory %s", options.dir)
	}

	glog.Infof("starting the %s, %s", program, version)
	kvClient := kv.New()

	vaultUrl, err := getVault(ctx, options.subscriptionId, options.vaultName, options.resourceGroup)
	if err != nil {
		return errors.Wrap(err, "failed to get vault")
	}

	token, err := GetKeyvaultToken(AuthGrantType(), options.cloudName, options.tenantId, options.usePodIdentity, options.aADClientSecret, options.aADClientID, options.podName, options.podNamespace)
	if err != nil {
		return errors.Wrapf(err, "failed to get key vault token")
	}

	kvClient.Authorizer = token

	objectTypes := strings.Split(options.vaultObjectTypes, objectsSep)
	objectNames := strings.Split(options.vaultObjectNames, objectsSep)
	numOfObjects := len(objectNames)

	// objectVersions are optional so we take as much as we can
	objectVersions := make([]string, numOfObjects)
	for index, value := range strings.Split(options.vaultObjectVersions, objectsSep) {
		objectVersions[index] = value
	}

	for i := 0; i < numOfObjects; i++ {
		objectType := objectTypes[i]
		objectName := objectNames[i]
		objectVersion := objectVersions[i]

		glog.V(0).Infof("retrieving %s %s (version: %s)", objectType, objectName, objectVersion)
		switch objectType {
		case VaultTypeSecret:
			secret, err := kvClient.GetSecret(ctx, *vaultUrl, objectName, objectVersion)
			if err != nil {
				return wrapObjectTypeError(err, objectType, objectName, objectVersion)
			}
			return writeContent([]byte(*secret.Value), objectType, objectName)
		case VaultTypeKey:
			keybundle, err := kvClient.GetKey(ctx, *vaultUrl, objectName, objectVersion)
			if err != nil {
				return wrapObjectTypeError(err, objectType, objectName, objectVersion)
			}
			// NOTE: we are writing the RSA modulus content of the key
			return writeContent([]byte(*keybundle.Key.N), objectType, objectName)
		case VaultTypeCertificate:
			certbundle, err := kvClient.GetCertificate(ctx, *vaultUrl, objectName, objectVersion)
			if err != nil {
				return wrapObjectTypeError(err, objectType, objectName, objectVersion)
			}
			return writeContent(*certbundle.Cer, objectType, objectName)
		default:
			return errors.Errorf("Invalid vaultObjectTypes. Should be secret, key, or cert")
		}
	}

	return nil
}

func wrapObjectTypeError(err error, objectType string, objectName string, objectVersion string) error {
	return errors.Wrapf(err, "failed to get objectType:%s, objetName:%s, objectVersion:%s", objectType, objectName, objectVersion)
}

func writeContent(objectContent []byte, objectType string, objectName string) error {
	if err := ioutil.WriteFile(path.Join(options.dir, objectName), objectContent, permission); err != nil {
		return errors.Wrapf(err, "azure KeyVault failed to write %s %s at %s", objectType, objectName, options.dir)
	}
	glog.V(0).Infof("azure KeyVault wrote %s %s at %s", objectType, objectName, options.dir)
	return nil
}

func parseConfigs() error {
	flag.StringVar(&options.vaultName, "vaultName", "", "Name of Azure Key Vault instance.")
	flag.StringVar(&options.vaultObjectNames, "vaultObjectNames", "", "Names of Azure Key Vault objects, semi-colon separated.")
	flag.StringVar(&options.vaultObjectTypes, "vaultObjectTypes", "", "Types of Azure Key Vault objects, semi-colon separated.")
	flag.StringVar(&options.vaultObjectVersions, "vaultObjectVersions", "", "Versions of Azure Key Vault objects, semi-colon separated.")
	flag.StringVar(&options.resourceGroup, "resourceGroup", "", "Resource group name of Azure Key Vault.")
	flag.StringVar(&options.subscriptionId, "subscriptionId", "", "subscriptionId to Azure.")
	flag.StringVar(&options.aADClientID, "aADClientID", "", "aADClientID to Azure.")
	flag.StringVar(&options.aADClientSecret, "aADClientSecret", "", "aADClientSecret to Azure.")
	flag.StringVar(&options.cloudName, "cloudName", "", "Type of Azure cloud")
	flag.StringVar(&options.tenantId, "tenantId", "", "tenantId to Azure")
	flag.BoolVar(&options.usePodIdentity, "usePodIdentity", false, "usePodIdentity for using pod identity.")
	flag.StringVar(&options.dir, "dir", "", "Directory path to write data.")
	flag.BoolVar(&options.showVersion, "version", true, "Show version.")
	flag.StringVar(&options.podName, "podName", "", "Name of the pod")
	flag.StringVar(&options.podNamespace, "podNamespace", "", "Namespace of the pod")

	flag.Parse()
	fmt.Println(options.vaultName)

	if options.vaultName == "" {
		return fmt.Errorf("-vaultName is not set")
	}

	if options.vaultObjectNames == "" {
		return fmt.Errorf("-vaultObjectNames is not set")
	}

	if options.resourceGroup == "" {
		return fmt.Errorf("-resourceGroup is not set")
	}

	if options.subscriptionId == "" {
		return fmt.Errorf("-subscriptionId is not set")
	}

	if options.dir == "" {
		return fmt.Errorf("-dir is not set")
	}

	if options.tenantId == "" {
		return fmt.Errorf("-tenantId is not set")
	}

	if strings.Count(options.vaultObjectNames, objectsSep) !=
		strings.Count(options.vaultObjectTypes, objectsSep) {
		return fmt.Errorf("-vaultObjectNames and -vaultObjectTypes are not matching")
	}

	if options.usePodIdentity == false {
		if options.aADClientID == "" {
			return fmt.Errorf("-aADClientID is not set")
		}
		if options.aADClientSecret == "" {
			return fmt.Errorf("-aADClientSecret is not set")
		}
	} else {
		if options.podName == "" {
			return fmt.Errorf("-podName is not set")
		}
		if options.podNamespace == "" {
			return fmt.Errorf("-podNamespace is not set")
		}
	}

	// validate all object types
	for _, objectType := range strings.Split(options.vaultObjectTypes, objectsSep) {
		if objectType != VaultTypeSecret && objectType != VaultTypeKey && objectType != VaultTypeCertificate {
			return fmt.Errorf("-vaultObjectType is invalid, should be set to secret, key, or certificate")
		}
	}
	return nil
}

func showUsage(message string, args ...interface{}) {
	flag.PrintDefaults()
	if message != "" {
		fmt.Printf("\n[error] "+message+"\n", args...)
	}
}

func showError(message string, args ...interface{}) {
	if message != "" {
		fmt.Printf("\n[error] "+message+"\n", args...)
	}
}

func getVault(ctx context.Context, subscriptionID string, vaultName string, resourceGroup string) (vaultUrl *string, err error) {
	glog.Infof("subscriptionID: %s", subscriptionID)
	glog.Infof("vaultName: %s", vaultName)
	glog.Infof("resourceGroup: %s", resourceGroup)

	vaultsClient := kvmgmt.NewVaultsClient(subscriptionID)
	token, _ := GetManagementToken(AuthGrantType(), options.cloudName, options.tenantId, options.usePodIdentity, options.aADClientSecret, options.aADClientID, options.podName, options.podNamespace)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get management token")
	}
	vaultsClient.Authorizer = token
	vault, err := vaultsClient.Get(ctx, resourceGroup, vaultName)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get vault %s", vaultName)
	}
	return vault.Properties.VaultURI, nil
}
