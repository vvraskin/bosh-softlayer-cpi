package vm

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"
	boshuuid "github.com/cloudfoundry/bosh-utils/uuid"

	common "github.com/maximilien/bosh-softlayer-cpi/common"
	bslcommon "github.com/maximilien/bosh-softlayer-cpi/softlayer/common"
	bslcstem "github.com/maximilien/bosh-softlayer-cpi/softlayer/stemcell"
	bslcvmpool "github.com/maximilien/bosh-softlayer-cpi/softlayer/vm/pool"
	sl "github.com/maximilien/softlayer-go/softlayer"

	util "github.com/maximilien/bosh-softlayer-cpi/util"
)

const softLayerCreatorLogTag = "SoftLayerCreator"

type SoftLayerCreator struct {
	softLayerClient        sl.Client
	agentEnvServiceFactory AgentEnvServiceFactory

	agentOptions  AgentOptions
	logger        boshlog.Logger
	uuidGenerator boshuuid.Generator
	fs            boshsys.FileSystem
}

func NewSoftLayerCreator(softLayerClient sl.Client, agentEnvServiceFactory AgentEnvServiceFactory, agentOptions AgentOptions, logger boshlog.Logger, uuidGenerator boshuuid.Generator, fs boshsys.FileSystem) SoftLayerCreator {
	bslcommon.TIMEOUT = 60 * time.Minute
	bslcommon.POLLING_INTERVAL = 10 * time.Second

	return SoftLayerCreator{
		softLayerClient:        softLayerClient,
		agentEnvServiceFactory: agentEnvServiceFactory,
		agentOptions:           agentOptions,
		logger:                 logger,
		uuidGenerator:          uuidGenerator,
		fs:                     fs,
	}
}

func (c SoftLayerCreator) CreateNewVM(agentID string, stemcell bslcstem.Stemcell, cloudProps VMCloudProperties, networks Networks, env Environment) (VM, error) {

	virtualGuestTemplate, err := CreateVirtualGuestTemplate(stemcell, cloudProps)

	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Creating virtual guest template")
	}

	virtualGuestService, err := c.softLayerClient.GetSoftLayer_Virtual_Guest_Service()
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Creating VirtualGuestService from SoftLayer client")
	}

	virtualGuest, err := virtualGuestService.CreateObject(virtualGuestTemplate)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Creating VirtualGuest from SoftLayer client")
	}

	if cloudProps.EphemeralDiskSize == 0 {
		err = bslcommon.WaitForVirtualGuestLastCompleteTransaction(c.softLayerClient, virtualGuest.Id, "Service Setup")
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapErrorf(err, "Waiting for VirtualGuest `%d` has Service Setup transaction complete", virtualGuest.Id)
		}
	} else {
		err = bslcommon.AttachEphemeralDiskToVirtualGuest(c.softLayerClient, virtualGuest.Id, cloudProps.EphemeralDiskSize, c.logger)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapError(err, fmt.Sprintf("Attaching ephemeral disk to VirtualGuest `%d`", virtualGuest.Id))
		}
	}

	virtualGuest, err = bslcommon.GetObjectDetailsOnVirtualGuest(c.softLayerClient, virtualGuest.Id)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot get details from virtual guest with id: %d.", virtualGuest.Id)
	}

	softlayerFileService := NewSoftlayerFileService(util.GetSshClient(), virtualGuest, c.logger, c.uuidGenerator, c.fs)
	agentEnvService := c.agentEnvServiceFactory.New(softlayerFileService, strconv.Itoa(virtualGuest.Id))

	if len(cloudProps.BoshIp) == 0 {
		// update /etc/hosts file of bosh-init vm
		c.updateEtcHostsOfBoshInit(fmt.Sprintf("%s  %s", virtualGuest.PrimaryBackendIpAddress, virtualGuest.FullyQualifiedDomainName))
		// Update mbus url setting for bosh director: construct mbus url with new director ip
		mbus, err := c.parseMbusURL(c.agentOptions.Mbus, virtualGuest.PrimaryBackendIpAddress)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot construct mbus url.")
		}
		c.agentOptions.Mbus = mbus
	} else {
		// Update mbus url setting
		mbus, err := c.parseMbusURL(c.agentOptions.Mbus, cloudProps.BoshIp)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot construct mbus url.")
		}
		c.agentOptions.Mbus = mbus
		// Update blobstore setting
		switch c.agentOptions.Blobstore.Type {
		case BlobstoreTypeDav:
			davConf := DavConfig(c.agentOptions.Blobstore.Options)
			c.updateDavConfig(&davConf, cloudProps.BoshIp)
		}
	}

	agentEnv := CreateAgentUserData(agentID, cloudProps, networks, env, c.agentOptions)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot agent env for virtual guest with id: %d.", virtualGuest.Id)
	}

	err = agentEnvService.Update(agentEnv)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Updating VM's agent env")
	}

	vm := NewSoftLayerVM(virtualGuest.Id, c.softLayerClient, util.GetSshClient(), agentEnvService, c.logger)

	if strings.ToUpper(common.GetOSEnvVariable("OS_RELOAD_ENABLED", "TRUE")) == "TRUE" && !strings.Contains(cloudProps.VmNamePrefix, "-worker") {
		db, err := bslcvmpool.OpenDB(bslcvmpool.SQLITE_DB_FILE_PATH)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapError(err, "Opening DB")
		}

		vmInfoDB := bslcvmpool.NewVMInfoDB(vm.ID(), virtualGuestTemplate.Hostname+"."+virtualGuestTemplate.Domain, "t", stemcell.Uuid(), agentID, c.logger, db)
		err = vmInfoDB.InsertVMInfo(bslcvmpool.DB_RETRY_TIMEOUT, bslcvmpool.DB_RETRY_INTERVAL)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapError(err, "Failed to insert the record into VM pool DB")
		}
	}

	return vm, nil
}

func (c SoftLayerCreator) Create(agentID string, stemcell bslcstem.Stemcell, cloudProps VMCloudProperties, networks Networks, env Environment) (VM, error) {
	if strings.ToUpper(common.GetOSEnvVariable("OS_RELOAD_ENABLED", "TRUE")) == "FALSE" {
		return c.CreateNewVM(agentID, stemcell, cloudProps, networks, env)
	}

	err := bslcvmpool.InitVMPoolDB(bslcvmpool.DB_RETRY_TIMEOUT, bslcvmpool.DB_RETRY_INTERVAL, c.logger)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Failed to initialize VM pool DB")
	}

	if strings.Contains(cloudProps.VmNamePrefix, "-worker") {
		vm, err := c.CreateNewVM(agentID, stemcell, cloudProps, networks, env)
		return vm, err
	}

	db, err := bslcvmpool.OpenDB(bslcvmpool.SQLITE_DB_FILE_PATH)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Opening DB")
	}

	vmInfoDB := bslcvmpool.NewVMInfoDB(0, "", "f", "", agentID, c.logger, db)
	defer vmInfoDB.CloseDB()

	err = vmInfoDB.QueryVMInfobyAgentID(bslcvmpool.DB_RETRY_TIMEOUT, bslcvmpool.DB_RETRY_INTERVAL)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Failed to query VM info by given agent ID "+agentID)
	}

	if vmInfoDB.VmProperties.Id != 0 {
		c.logger.Info(softLayerCreatorLogTag, fmt.Sprintf("OS reload on the server id %d with stemcell %d", vmInfoDB.VmProperties.Id, stemcell.ID()))

		vm := NewSoftLayerVM(vmInfoDB.VmProperties.Id, c.softLayerClient, util.GetSshClient(), nil, c.logger)

		bslcommon.TIMEOUT = 24 * time.Hour
		err = vm.ReloadOS(stemcell)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapError(err, "Failed to reload OS")
		}

		if cloudProps.EphemeralDiskSize == 0 {
			err = bslcommon.WaitForVirtualGuestLastCompleteTransaction(c.softLayerClient, vm.ID(), "Service Setup")
			if err != nil {
				return SoftLayerVM{}, bosherr.WrapErrorf(err, "Waiting for VirtualGuest `%d` has Service Setup transaction complete", vm.ID())
			}
		} else {
			err = bslcommon.AttachEphemeralDiskToVirtualGuest(c.softLayerClient, vm.ID(), cloudProps.EphemeralDiskSize, c.logger)
			if err != nil {
				return SoftLayerVM{}, bosherr.WrapError(err, fmt.Sprintf("Attaching ephemeral disk to VirtualGuest `%d`", vm.ID()))
			}
		}

		virtualGuest, err := bslcommon.GetObjectDetailsOnVirtualGuest(c.softLayerClient, vmInfoDB.VmProperties.Id)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot get details from virtual guest with id: %d.", virtualGuest.Id)
		}

		softlayerFileService := NewSoftlayerFileService(util.GetSshClient(), virtualGuest, c.logger, c.uuidGenerator, c.fs)
		agentEnvService := c.agentEnvServiceFactory.New(softlayerFileService, strconv.Itoa(virtualGuest.Id))

		agentEnv := CreateAgentUserData(agentID, cloudProps, networks, env, c.agentOptions)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot agent env for virtual guest with id: %d.", virtualGuest.Id)
		}

		if len(cloudProps.BoshIp) == 0 {
			// update /etc/hosts file of bosh-init vm
			c.updateEtcHostsOfBoshInit(fmt.Sprintf("%s  %s", virtualGuest.PrimaryBackendIpAddress, virtualGuest.FullyQualifiedDomainName))
			// Update mbus url setting for bosh director: construct mbus url with new director ip
			mbus, err := c.parseMbusURL(c.agentOptions.Mbus, virtualGuest.PrimaryBackendIpAddress)
			if err != nil {
				return SoftLayerVM{}, bosherr.WrapErrorf(err, "Cannot construct mbus url.")
			}
			agentEnv.Mbus = mbus
		}

		err = agentEnvService.Update(agentEnv)
		if err != nil {
			return SoftLayerVM{}, bosherr.WrapError(err, "Updating VM's agent env")
		}

		vm = NewSoftLayerVM(virtualGuest.Id, c.softLayerClient, util.GetSshClient(), agentEnvService, c.logger)

		c.logger.Info(softLayerCreatorLogTag, fmt.Sprintf("Updated in_use flag to 't' for the VM %d in VM pool", vmInfoDB.VmProperties.Id))
		vmInfoDB.VmProperties.InUse = "t"
		err = vmInfoDB.UpdateVMInfoByID(bslcvmpool.DB_RETRY_TIMEOUT, bslcvmpool.DB_RETRY_INTERVAL)
		if err != nil {
			return vm, bosherr.WrapError(err, fmt.Sprintf("Failed to query VM info by given ID %d", vm.ID()))
		} else {
			return vm, nil
		}

	}

	vmInfoDB.VmProperties.InUse = ""
	err = vmInfoDB.QueryVMInfobyAgentID(bslcvmpool.DB_RETRY_TIMEOUT, bslcvmpool.DB_RETRY_INTERVAL)
	if err != nil {
		return SoftLayerVM{}, bosherr.WrapError(err, "Failed to query VM info by given agent ID "+agentID)
	}

	if vmInfoDB.VmProperties.Id != 0 {
		return SoftLayerVM{}, bosherr.WrapError(err, "Wrong in_use status in VM with agent ID "+agentID+", Do not create a new VM")
	} else {
		return c.CreateNewVM(agentID, stemcell, cloudProps, networks, env)
	}

}

// Private methods
func (c SoftLayerCreator) updateDavConfig(config *DavConfig, directorIP string) (err error) {
	url := (*config)["endpoint"].(string)
	mbus, err := c.parseMbusURL(url, directorIP)
	if err != nil {
		return bosherr.WrapError(err, "Parsing Mbus URL")
	}

	(*config)["endpoint"] = mbus

	return nil
}

func (c SoftLayerCreator) parseMbusURL(mbusURL string, primaryBackendIpAddress string) (string, error) {
	parsedURL, err := url.Parse(mbusURL)
	if err != nil {
		return "", bosherr.WrapError(err, "Parsing Mbus URL")
	}
	var username, password, port string
	_, port, _ = net.SplitHostPort(parsedURL.Host)
	userInfo := parsedURL.User
	if userInfo != nil {
		username = userInfo.Username()
		password, _ = userInfo.Password()
		return fmt.Sprintf("%s://%s:%s@%s:%s", parsedURL.Scheme, username, password, primaryBackendIpAddress, port), nil
	}

	return fmt.Sprintf("%s://%s:%s", parsedURL.Scheme, primaryBackendIpAddress, port), nil
}

func (c SoftLayerCreator) updateEtcHostsOfBoshInit(record string) (err error) {
	buffer := bytes.NewBuffer([]byte{})
	t := template.Must(template.New("etc-hosts").Parse(ETC_HOSTS_TEMPLATE))

	err = t.Execute(buffer, record)
	if err != nil {
		return bosherr.WrapError(err, "Generating config from template")
	}

	err = c.fs.WriteFile("/etc/hosts", buffer.Bytes())
	if err != nil {
		return bosherr.WrapError(err, "Writing to /etc/hosts")
	}

	return nil
}

const ETC_HOSTS_TEMPLATE = `127.0.0.1 localhost
{{.}}
`

func (c SoftLayerCreator) appendRecordToEtcHosts(record string) (err error) {
	file, err := os.OpenFile("/etc/hosts", os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return bosherr.WrapError(err, "Failed to open file /etc/hosts")
	}

	defer file.Close()

	_, err = fmt.Fprintf(file, "\n %s", record)
	if err != nil {
		return bosherr.WrapErrorf(err, "Failed to append new record to /etc/hosts")
	}

	return nil
}
