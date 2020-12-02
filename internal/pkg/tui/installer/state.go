// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package installer contains terminal UI based talos interactive installer parts.
package installer

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/rivo/tview"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/talos-systems/talos/internal/pkg/tui/components"
	"github.com/talos-systems/talos/pkg/images"
	machineapi "github.com/talos-systems/talos/pkg/machinery/api/machine"
	"github.com/talos-systems/talos/pkg/machinery/api/network"
	"github.com/talos-systems/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/talos-systems/talos/pkg/machinery/config/types/v1alpha1/machine"
	"github.com/talos-systems/talos/pkg/machinery/constants"
)

// cniPresets defines custom CNI presets.
var cniPresets = map[string]*machineapi.CNIConfig{
	"cilium": {
		Name: "custom",
		Urls: []string{
			"https://raw.githubusercontent.com/cilium/cilium/v1.8/install/kubernetes/quick-install.yaml",
		},
	},
}

// NewState creates new installer state.
// nolint:gocyclo
func NewState(ctx context.Context, installer *Installer, conn *Connection) (*State, error) {
	opts := &machineapi.GenerateConfigurationRequest{
		ConfigVersion: "v1alpha1",
		MachineConfig: &machineapi.MachineConfig{
			Type:              machineapi.MachineConfig_MachineType(machine.TypeInit),
			NetworkConfig:     &machineapi.NetworkConfig{},
			KubernetesVersion: constants.DefaultKubernetesVersion,
			InstallConfig: &machineapi.InstallConfig{
				InstallImage: images.DefaultInstallerImage,
			},
		},
		ClusterConfig: &machineapi.ClusterConfig{
			Name:         "talos-default",
			ControlPlane: &machineapi.ControlPlaneConfig{},
			ClusterNetwork: &machineapi.ClusterNetworkConfig{
				DnsDomain: "cluster.local",
			},
		},
	}

	if conn.ExpandingCluster() {
		opts.ClusterConfig.ControlPlane.Endpoint = fmt.Sprintf("https://%s:%d", conn.bootstrapEndpoint, constants.DefaultControlPlanePort)
	} else {
		opts.ClusterConfig.ControlPlane.Endpoint = fmt.Sprintf("https://%s:%d", conn.nodeEndpoint, constants.DefaultControlPlanePort)
	}

	installDiskOptions := []interface{}{
		components.NewTableHeaders("DEVICE NAME", "MODEL NAME", "SIZE"),
	}

	disks, err := conn.Disks()
	if err != nil {
		return nil, err
	}

	for i, disk := range disks.Disks {
		if i == 0 {
			opts.MachineConfig.InstallConfig.InstallDisk = disk.DeviceName
		}

		installDiskOptions = append(installDiskOptions, disk.DeviceName, disk.Model, humanize.Bytes(disk.Size))
	}

	var machineTypes []interface{}

	if conn.ExpandingCluster() {
		machineTypes = []interface{}{
			" worker ", machineapi.MachineConfig_MachineType(machine.TypeJoin),
			" control plane ", machineapi.MachineConfig_MachineType(machine.TypeControlPlane),
		}
		opts.MachineConfig.Type = machineapi.MachineConfig_MachineType(machine.TypeControlPlane)
	} else {
		machineTypes = []interface{}{
			" control plane ", machineapi.MachineConfig_MachineType(machine.TypeInit),
		}
	}

	state := &State{
		cni:  constants.DefaultCNI,
		conn: conn,
		opts: opts,
	}

	networkConfigItems := []*components.Item{
		components.NewItem(
			"Hostname",
			v1alpha1.NetworkConfigDoc.Describe("hostname", true),
			&opts.MachineConfig.NetworkConfig.Hostname,
		),
		components.NewItem(
			"DNS Domain",
			v1alpha1.ClusterNetworkConfigDoc.Describe("dnsDomain", true),
			&opts.ClusterConfig.ClusterNetwork.DnsDomain,
		),
	}

	interfaces, err := conn.Interfaces()
	if err != nil {
		return nil, err
	}

	addedInterfaces := false
	opts.MachineConfig.NetworkConfig.Interfaces = []*machineapi.NetworkDeviceConfig{}

	for _, iface := range interfaces.Messages[0].Interfaces {
		status := ""

		if (net.Flags(iface.Flags) & net.FlagUp) != 0 {
			status = " (UP)"
		}

		if !addedInterfaces {
			networkConfigItems = append(networkConfigItems, components.NewSeparator("Network Interfaces Configuration"))
			addedInterfaces = true
		}

		networkConfigItems = append(networkConfigItems, components.NewItem(
			fmt.Sprintf("%s, %s%s", iface.Name, iface.Hardwareaddr, status),
			"",
			configureAdapter(installer, opts, iface),
		))
	}

	if !conn.ExpandingCluster() {
		networkConfigItems = append(networkConfigItems,
			components.NewSeparator(v1alpha1.ClusterNetworkConfigDoc.Describe("cni", true)),
			components.NewItem(
				"Type",
				v1alpha1.ClusterNetworkConfigDoc.Describe("cni", true),
				&state.cni,
				components.NewTableHeaders("CNI", "description"),
				constants.DefaultCNI, "CNI used by Talos by default",
				"cilium", "Cillium 1.8 installed through quick-install.yaml",
			))
	}

	state.pages = []*Page{
		NewPage("Installer Params",
			components.NewItem(
				"Image",
				v1alpha1.InstallConfigDoc.Describe("image", true),
				&opts.MachineConfig.InstallConfig.InstallImage,
			),
			components.NewSeparator(
				v1alpha1.InstallConfigDoc.Describe("disk", true),
			),
			components.NewItem(
				"Install Disk",
				"",
				&opts.MachineConfig.InstallConfig.InstallDisk,
				installDiskOptions...,
			),
		),
		NewPage("Machine Config",
			components.NewItem(
				"Machine Type",
				v1alpha1.MachineConfigDoc.Describe("type", true),
				&opts.MachineConfig.Type,
				machineTypes...,
			),
			components.NewItem(
				"Cluster Name",
				v1alpha1.ClusterConfigDoc.Describe("clusterName", true),
				&opts.ClusterConfig.Name,
			),
			components.NewItem(
				"Control Plane Endpoint",
				v1alpha1.ControlPlaneConfigDoc.Describe("endpoint", true),
				&opts.ClusterConfig.ControlPlane.Endpoint,
			),
			components.NewItem(
				"Kubernetes Version",
				"",
				&opts.MachineConfig.KubernetesVersion,
			),
		),
		NewPage("Network Config",
			networkConfigItems...,
		),
	}

	return state, nil
}

// State installer state.
type State struct {
	pages []*Page
	opts  *machineapi.GenerateConfigurationRequest
	conn  *Connection
	cni   string
}

// GenConfig returns current config encoded in yaml.
func (s *State) GenConfig() (*machineapi.GenerateConfigurationResponse, error) {
	// configure custom cni from the preset
	if customCNI, ok := cniPresets[s.cni]; ok {
		s.opts.ClusterConfig.ClusterNetwork.CniConfig = customCNI
	}

	s.opts.OverrideTime = timestamppb.New(time.Now().UTC())

	return s.conn.GenerateConfiguration(s.opts)
}

func configureAdapter(installer *Installer, opts *machineapi.GenerateConfigurationRequest, adapter *network.Interface) func(item *components.Item) tview.Primitive {
	return func(item *components.Item) tview.Primitive {
		return components.NewFormModalButton(item.Name, "configure").
			SetSelectedFunc(func() {
				deviceIndex := -1
				var adapterSettings *machineapi.NetworkDeviceConfig

				for i, iface := range opts.MachineConfig.NetworkConfig.Interfaces {
					if iface.Interface == adapter.Name {
						deviceIndex = i
						adapterSettings = iface

						break
					}
				}

				if adapterSettings == nil {
					adapterSettings = &machineapi.NetworkDeviceConfig{
						Interface:   adapter.Name,
						Dhcp:        true,
						Mtu:         int32(adapter.Mtu),
						Ignore:      false,
						DhcpOptions: &machineapi.DHCPOptionsConfig{},
					}

					if len(adapter.Ipaddress) > 0 {
						adapterSettings.Cidr = adapter.Ipaddress[0]
					}
				}

				items := []*components.Item{
					components.NewItem(
						"Use DHCP",
						v1alpha1.DeviceDoc.Describe("dhcp", true),
						&adapterSettings.Dhcp,
					),
					components.NewItem(
						"Ignore",
						v1alpha1.DeviceDoc.Describe("ignore", true),
						&adapterSettings.Ignore,
					),
					components.NewItem(
						"CIDR",
						v1alpha1.DeviceDoc.Describe("cidr", true),
						&adapterSettings.Cidr,
					),
					components.NewItem(
						"MTU",
						v1alpha1.DeviceDoc.Describe("mtu", true),
						&adapterSettings.Mtu,
					),
					components.NewItem(
						"Route Metric",
						v1alpha1.DeviceDoc.Describe("dhcpOptions", true),
						&adapterSettings.DhcpOptions.RouteMetric,
					),
				}

				adapterConfiguration := components.NewForm(installer.app)
				if err := adapterConfiguration.AddFormItems(items); err != nil {
					panic(err)
				}

				focused := installer.app.GetFocus()
				page, _ := installer.pages.GetFrontPage()

				goBack := func() {
					installer.pages.SwitchToPage(page)
					installer.app.SetFocus(focused)
				}

				adapterConfiguration.AddMenuButton("Cancel", false).SetSelectedFunc(func() {
					goBack()
				})

				adapterConfiguration.AddMenuButton("Apply", false).SetSelectedFunc(func() {
					goBack()

					if deviceIndex == -1 {
						opts.MachineConfig.NetworkConfig.Interfaces = append(
							opts.MachineConfig.NetworkConfig.Interfaces,
							adapterSettings,
						)
					}
				})

				flex := tview.NewFlex().SetDirection(tview.FlexRow)
				flex.AddItem(tview.NewBox().SetBackgroundColor(color), 1, 0, false)
				flex.AddItem(adapterConfiguration, 0, 1, false)

				installer.addPage(
					fmt.Sprintf("Adapter %s Configuration", adapter.Name),
					flex,
					true,
					nil,
				)
				installer.app.SetFocus(adapterConfiguration)
			})
	}
}
