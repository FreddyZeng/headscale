package integration

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	policyv1 "github.com/juanfont/headscale/hscontrol/policy/v1"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/juanfont/headscale/integration/hsic"
	"github.com/juanfont/headscale/integration/tsic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var veryLargeDestination = []string{
	"0.0.0.0/5:*",
	"8.0.0.0/7:*",
	"11.0.0.0/8:*",
	"12.0.0.0/6:*",
	"16.0.0.0/4:*",
	"32.0.0.0/3:*",
	"64.0.0.0/2:*",
	"128.0.0.0/3:*",
	"160.0.0.0/5:*",
	"168.0.0.0/6:*",
	"172.0.0.0/12:*",
	"172.32.0.0/11:*",
	"172.64.0.0/10:*",
	"172.128.0.0/9:*",
	"173.0.0.0/8:*",
	"174.0.0.0/7:*",
	"176.0.0.0/4:*",
	"192.0.0.0/9:*",
	"192.128.0.0/11:*",
	"192.160.0.0/13:*",
	"192.169.0.0/16:*",
	"192.170.0.0/15:*",
	"192.172.0.0/14:*",
	"192.176.0.0/12:*",
	"192.192.0.0/10:*",
	"193.0.0.0/8:*",
	"194.0.0.0/7:*",
	"196.0.0.0/6:*",
	"200.0.0.0/5:*",
	"208.0.0.0/4:*",
}

func aclScenario(
	t *testing.T,
	policy *policyv1.ACLPolicy,
	clientsPerUser int,
) *Scenario {
	t.Helper()

	spec := ScenarioSpec{
		NodesPerUser: clientsPerUser,
		Users:        []string{"user1", "user2"},
	}

	scenario, err := NewScenario(spec)
	require.NoError(t, err)

	err = scenario.CreateHeadscaleEnv(
		[]tsic.Option{
			// Alpine containers dont have ip6tables set up, which causes
			// tailscaled to stop configuring the wgengine, causing it
			// to not configure DNS.
			tsic.WithNetfilter("off"),
			tsic.WithDockerEntrypoint([]string{
				"/bin/sh",
				"-c",
				"/bin/sleep 3 ; apk add python3 curl ; update-ca-certificates ; python3 -m http.server --bind :: 80 & tailscaled --tun=tsdev",
			}),
			tsic.WithDockerWorkdir("/"),
		},
		hsic.WithACLPolicy(policy),
		hsic.WithTestName("acl"),
		hsic.WithEmbeddedDERPServerOnly(),
		hsic.WithTLS(),
	)
	require.NoError(t, err)

	_, err = scenario.ListTailscaleClientsFQDNs()
	require.NoError(t, err)

	return scenario
}

// This tests a different ACL mechanism, if a host _cannot_ connect
// to another node at all based on ACL, it should just not be part
// of the NetMap sent to the host. This is slightly different than
// the other tests as we can just check if the hosts are present
// or not.
func TestACLHostsInNetMapTable(t *testing.T) {
	IntegrationSkip(t)

	spec := ScenarioSpec{
		NodesPerUser: 2,
		Users:        []string{"user1", "user2"},
	}

	// NOTE: All want cases currently checks the
	// total count of expected peers, this would
	// typically be the client count of the users
	// they can access minus one (them self).
	tests := map[string]struct {
		users  ScenarioSpec
		policy policyv1.ACLPolicy
		want   map[string]int
	}{
		// Test that when we have no ACL, each client netmap has
		// the amount of peers of the total amount of clients
		"base-acls": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"*"},
						Destinations: []string{"*:*"},
					},
				},
			}, want: map[string]int{
				"user1@test.no": 3, // ns1 + ns2
				"user2@test.no": 3, // ns2 + ns1
			},
		},
		// Test that when we have two users, which cannot see
		// each other, each node has only the number of pairs from
		// their own user.
		"two-isolated-users": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: []string{"user1@:*"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user2@"},
						Destinations: []string{"user2@:*"},
					},
				},
			}, want: map[string]int{
				"user1@test.no": 1,
				"user2@test.no": 1,
			},
		},
		// Test that when we have two users, with ACLs and they
		// are restricted to a single port, nodes are still present
		// in the netmap.
		"two-restricted-present-in-netmap": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: []string{"user1@:22"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user2@"},
						Destinations: []string{"user2@:22"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: []string{"user2@:22"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user2@"},
						Destinations: []string{"user1@:22"},
					},
				},
			}, want: map[string]int{
				"user1@test.no": 3,
				"user2@test.no": 3,
			},
		},
		// Test that when we have two users, that are isolated,
		// but one can see the others, we have the appropriate number
		// of peers. This will still result in all the peers as we
		// need them present on the other side for the "return path".
		"two-ns-one-isolated": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: []string{"user1@:*"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user2@"},
						Destinations: []string{"user2@:*"},
					},
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: []string{"user2@:*"},
					},
				},
			}, want: map[string]int{
				"user1@test.no": 3, // ns1 + ns2
				"user2@test.no": 3, // ns1 + ns2 (return path)
			},
		},
		"very-large-destination-prefix-1372": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: append([]string{"user1@:*"}, veryLargeDestination...),
					},
					{
						Action:       "accept",
						Sources:      []string{"user2@"},
						Destinations: append([]string{"user2@:*"}, veryLargeDestination...),
					},
					{
						Action:       "accept",
						Sources:      []string{"user1@"},
						Destinations: append([]string{"user2@:*"}, veryLargeDestination...),
					},
				},
			}, want: map[string]int{
				"user1@test.no": 3, // ns1 + ns2
				"user2@test.no": 3, // ns1 + ns2 (return path)
			},
		},
		"ipv6-acls-1470": {
			users: spec,
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"*"},
						Destinations: []string{"0.0.0.0/0:*", "::/0:*"},
					},
				},
			}, want: map[string]int{
				"user1@test.no": 3, // ns1 + ns2
				"user2@test.no": 3, // ns2 + ns1
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			caseSpec := testCase.users
			scenario, err := NewScenario(caseSpec)
			require.NoError(t, err)

			err = scenario.CreateHeadscaleEnv(
				[]tsic.Option{},
				hsic.WithACLPolicy(&testCase.policy),
			)
			require.NoError(t, err)
			defer scenario.ShutdownAssertNoPanics(t)

			allClients, err := scenario.ListTailscaleClients()
			require.NoError(t, err)

			err = scenario.WaitForTailscaleSyncWithPeerCount(testCase.want["user1@test.no"])
			require.NoError(t, err)

			for _, client := range allClients {
				status, err := client.Status()
				require.NoError(t, err)

				user := status.User[status.Self.UserID].LoginName

				assert.Len(t, status.Peer, (testCase.want[user]))
			}
		})
	}
}

// Test to confirm that we can use user:80 from one user
// This should make the node appear in the peer list, but
// disallow ping.
// This ACL will not allow user1 access its own machines.
// Reported: https://github.com/juanfont/headscale/issues/699
func TestACLAllowUser80Dst(t *testing.T) {
	IntegrationSkip(t)

	scenario := aclScenario(t,
		&policyv1.ACLPolicy{
			ACLs: []policyv1.ACL{
				{
					Action:       "accept",
					Sources:      []string{"user1@"},
					Destinations: []string{"user2@:80"},
				},
			},
		},
		1,
	)
	defer scenario.ShutdownAssertNoPanics(t)

	user1Clients, err := scenario.ListTailscaleClients("user1")
	require.NoError(t, err)

	user2Clients, err := scenario.ListTailscaleClients("user2")
	require.NoError(t, err)

	// Test that user1 can visit all user2
	for _, client := range user1Clients {
		for _, peer := range user2Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	// Test that user2 _cannot_ visit user1
	for _, client := range user2Clients {
		for _, peer := range user1Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Empty(t, result)
			require.Error(t, err)
		}
	}
}

func TestACLDenyAllPort80(t *testing.T) {
	IntegrationSkip(t)

	scenario := aclScenario(t,
		&policyv1.ACLPolicy{
			Groups: map[string][]string{
				"group:integration-acl-test": {"user1@", "user2@"},
			},
			ACLs: []policyv1.ACL{
				{
					Action:       "accept",
					Sources:      []string{"group:integration-acl-test"},
					Destinations: []string{"*:22"},
				},
			},
		},
		4,
	)
	defer scenario.ShutdownAssertNoPanics(t)

	allClients, err := scenario.ListTailscaleClients()
	require.NoError(t, err)

	allHostnames, err := scenario.ListTailscaleClientsFQDNs()
	require.NoError(t, err)

	for _, client := range allClients {
		for _, hostname := range allHostnames {
			// We will always be allowed to check _self_ so shortcircuit
			// the test here.
			if strings.Contains(hostname, client.Hostname()) {
				continue
			}

			url := fmt.Sprintf("http://%s/etc/hostname", hostname)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Empty(t, result)
			require.Error(t, err)
		}
	}
}

// Test to confirm that we can use user:* from one user.
// This ACL will not allow user1 access its own machines.
// Reported: https://github.com/juanfont/headscale/issues/699
func TestACLAllowUserDst(t *testing.T) {
	IntegrationSkip(t)

	scenario := aclScenario(t,
		&policyv1.ACLPolicy{
			ACLs: []policyv1.ACL{
				{
					Action:       "accept",
					Sources:      []string{"user1@"},
					Destinations: []string{"user2@:*"},
				},
			},
		},
		2,
	)
	defer scenario.ShutdownAssertNoPanics(t)

	user1Clients, err := scenario.ListTailscaleClients("user1")
	require.NoError(t, err)

	user2Clients, err := scenario.ListTailscaleClients("user2")
	require.NoError(t, err)

	// Test that user1 can visit all user2
	for _, client := range user1Clients {
		for _, peer := range user2Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	// Test that user2 _cannot_ visit user1
	for _, client := range user2Clients {
		for _, peer := range user1Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Empty(t, result)
			require.Error(t, err)
		}
	}
}

// Test to confirm that we can use *:* from one user
// Reported: https://github.com/juanfont/headscale/issues/699
func TestACLAllowStarDst(t *testing.T) {
	IntegrationSkip(t)

	scenario := aclScenario(t,
		&policyv1.ACLPolicy{
			ACLs: []policyv1.ACL{
				{
					Action:       "accept",
					Sources:      []string{"user1@"},
					Destinations: []string{"*:*"},
				},
			},
		},
		2,
	)
	defer scenario.ShutdownAssertNoPanics(t)

	user1Clients, err := scenario.ListTailscaleClients("user1")
	require.NoError(t, err)

	user2Clients, err := scenario.ListTailscaleClients("user2")
	require.NoError(t, err)

	// Test that user1 can visit all user2
	for _, client := range user1Clients {
		for _, peer := range user2Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	// Test that user2 _cannot_ visit user1
	for _, client := range user2Clients {
		for _, peer := range user1Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Empty(t, result)
			require.Error(t, err)
		}
	}
}

// TestACLNamedHostsCanReachBySubnet is the same as
// TestACLNamedHostsCanReach, but it tests if we expand a
// full CIDR correctly. All routes should work.
func TestACLNamedHostsCanReachBySubnet(t *testing.T) {
	IntegrationSkip(t)

	scenario := aclScenario(t,
		&policyv1.ACLPolicy{
			Hosts: policyv1.Hosts{
				"all": netip.MustParsePrefix("100.64.0.0/24"),
			},
			ACLs: []policyv1.ACL{
				// Everyone can curl test3
				{
					Action:       "accept",
					Sources:      []string{"*"},
					Destinations: []string{"all:*"},
				},
			},
		},
		3,
	)
	defer scenario.ShutdownAssertNoPanics(t)

	user1Clients, err := scenario.ListTailscaleClients("user1")
	require.NoError(t, err)

	user2Clients, err := scenario.ListTailscaleClients("user2")
	require.NoError(t, err)

	// Test that user1 can visit all user2
	for _, client := range user1Clients {
		for _, peer := range user2Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	// Test that user2 can visit all user1
	for _, client := range user2Clients {
		for _, peer := range user1Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}
}

// This test aims to cover cases where individual hosts are allowed and denied
// access based on their assigned hostname
// https://github.com/juanfont/headscale/issues/941
//
//	ACL = [{
//			"DstPorts": [{
//				"Bits": null,
//				"IP": "100.64.0.3/32",
//				"Ports": {
//					"First": 0,
//					"Last": 65535
//				}
//			}],
//			"SrcIPs": ["*"]
//		}, {
//
//			"DstPorts": [{
//				"Bits": null,
//				"IP": "100.64.0.2/32",
//				"Ports": {
//					"First": 0,
//					"Last": 65535
//				}
//			}],
//			"SrcIPs": ["100.64.0.1/32"]
//		}]
//
//	ACL Cache Map= {
//		"*": {
//			"100.64.0.3/32": {}
//		},
//		"100.64.0.1/32": {
//			"100.64.0.2/32": {}
//		}
//	}
//
// https://github.com/juanfont/headscale/issues/941
// Additionally verify ipv6 behaviour, part of
// https://github.com/juanfont/headscale/issues/809
func TestACLNamedHostsCanReach(t *testing.T) {
	IntegrationSkip(t)

	tests := map[string]struct {
		policy policyv1.ACLPolicy
	}{
		"ipv4": {
			policy: policyv1.ACLPolicy{
				Hosts: policyv1.Hosts{
					"test1": netip.MustParsePrefix("100.64.0.1/32"),
					"test2": netip.MustParsePrefix("100.64.0.2/32"),
					"test3": netip.MustParsePrefix("100.64.0.3/32"),
				},
				ACLs: []policyv1.ACL{
					// Everyone can curl test3
					{
						Action:       "accept",
						Sources:      []string{"*"},
						Destinations: []string{"test3:*"},
					},
					// test1 can curl test2
					{
						Action:       "accept",
						Sources:      []string{"test1"},
						Destinations: []string{"test2:*"},
					},
				},
			},
		},
		"ipv6": {
			policy: policyv1.ACLPolicy{
				Hosts: policyv1.Hosts{
					"test1": netip.MustParsePrefix("fd7a:115c:a1e0::1/128"),
					"test2": netip.MustParsePrefix("fd7a:115c:a1e0::2/128"),
					"test3": netip.MustParsePrefix("fd7a:115c:a1e0::3/128"),
				},
				ACLs: []policyv1.ACL{
					// Everyone can curl test3
					{
						Action:       "accept",
						Sources:      []string{"*"},
						Destinations: []string{"test3:*"},
					},
					// test1 can curl test2
					{
						Action:       "accept",
						Sources:      []string{"test1"},
						Destinations: []string{"test2:*"},
					},
				},
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			scenario := aclScenario(t,
				&testCase.policy,
				2,
			)
			defer scenario.ShutdownAssertNoPanics(t)

			// Since user/users dont matter here, we basically expect that some clients
			// will be assigned these ips and that we can pick them up for our own use.
			test1ip4 := netip.MustParseAddr("100.64.0.1")
			test1ip6 := netip.MustParseAddr("fd7a:115c:a1e0::1")
			test1, err := scenario.FindTailscaleClientByIP(test1ip6)
			require.NoError(t, err)

			test1fqdn, err := test1.FQDN()
			require.NoError(t, err)
			test1ip4URL := fmt.Sprintf("http://%s/etc/hostname", test1ip4.String())
			test1ip6URL := fmt.Sprintf("http://[%s]/etc/hostname", test1ip6.String())
			test1fqdnURL := fmt.Sprintf("http://%s/etc/hostname", test1fqdn)

			test2ip4 := netip.MustParseAddr("100.64.0.2")
			test2ip6 := netip.MustParseAddr("fd7a:115c:a1e0::2")
			test2, err := scenario.FindTailscaleClientByIP(test2ip6)
			require.NoError(t, err)

			test2fqdn, err := test2.FQDN()
			require.NoError(t, err)
			test2ip4URL := fmt.Sprintf("http://%s/etc/hostname", test2ip4.String())
			test2ip6URL := fmt.Sprintf("http://[%s]/etc/hostname", test2ip6.String())
			test2fqdnURL := fmt.Sprintf("http://%s/etc/hostname", test2fqdn)

			test3ip4 := netip.MustParseAddr("100.64.0.3")
			test3ip6 := netip.MustParseAddr("fd7a:115c:a1e0::3")
			test3, err := scenario.FindTailscaleClientByIP(test3ip6)
			require.NoError(t, err)

			test3fqdn, err := test3.FQDN()
			require.NoError(t, err)
			test3ip4URL := fmt.Sprintf("http://%s/etc/hostname", test3ip4.String())
			test3ip6URL := fmt.Sprintf("http://[%s]/etc/hostname", test3ip6.String())
			test3fqdnURL := fmt.Sprintf("http://%s/etc/hostname", test3fqdn)

			// test1 can query test3
			result, err := test1.Curl(test3ip4URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3ip4URL,
				result,
			)
			require.NoError(t, err)

			result, err = test1.Curl(test3ip6URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3ip6URL,
				result,
			)
			require.NoError(t, err)

			result, err = test1.Curl(test3fqdnURL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3fqdnURL,
				result,
			)
			require.NoError(t, err)

			// test2 can query test3
			result, err = test2.Curl(test3ip4URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3ip4URL,
				result,
			)
			require.NoError(t, err)

			result, err = test2.Curl(test3ip6URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3ip6URL,
				result,
			)
			require.NoError(t, err)

			result, err = test2.Curl(test3fqdnURL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test3 with URL %s, expected hostname of 13 chars, got %s",
				test3fqdnURL,
				result,
			)
			require.NoError(t, err)

			// test3 cannot query test1
			result, err = test3.Curl(test1ip4URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test3.Curl(test1ip6URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test3.Curl(test1fqdnURL)
			assert.Empty(t, result)
			require.Error(t, err)

			// test3 cannot query test2
			result, err = test3.Curl(test2ip4URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test3.Curl(test2ip6URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test3.Curl(test2fqdnURL)
			assert.Empty(t, result)
			require.Error(t, err)

			// test1 can query test2
			result, err = test1.Curl(test2ip4URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test2 with URL %s, expected hostname of 13 chars, got %s",
				test2ip4URL,
				result,
			)

			require.NoError(t, err)
			result, err = test1.Curl(test2ip6URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test2 with URL %s, expected hostname of 13 chars, got %s",
				test2ip6URL,
				result,
			)
			require.NoError(t, err)

			result, err = test1.Curl(test2fqdnURL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test2 with URL %s, expected hostname of 13 chars, got %s",
				test2fqdnURL,
				result,
			)
			require.NoError(t, err)

			// test2 cannot query test1
			result, err = test2.Curl(test1ip4URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test2.Curl(test1ip6URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test2.Curl(test1fqdnURL)
			assert.Empty(t, result)
			require.Error(t, err)
		})
	}
}

// TestACLDevice1CanAccessDevice2 is a table driven test that aims to test
// the various ways to achieve a connection between device1 and device2 where
// device1 can access device2, but not the other way around. This can be
// viewed as one of the most important tests here as it covers most of the
// syntax that can be used.
//
// Before adding new taste cases, consider if it can be reduced to a case
// in this function.
func TestACLDevice1CanAccessDevice2(t *testing.T) {
	IntegrationSkip(t)

	tests := map[string]struct {
		policy policyv1.ACLPolicy
	}{
		"ipv4": {
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"100.64.0.1"},
						Destinations: []string{"100.64.0.2:*"},
					},
				},
			},
		},
		"ipv6": {
			policy: policyv1.ACLPolicy{
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"fd7a:115c:a1e0::1"},
						Destinations: []string{"fd7a:115c:a1e0::2:*"},
					},
				},
			},
		},
		"hostv4cidr": {
			policy: policyv1.ACLPolicy{
				Hosts: policyv1.Hosts{
					"test1": netip.MustParsePrefix("100.64.0.1/32"),
					"test2": netip.MustParsePrefix("100.64.0.2/32"),
				},
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"test1"},
						Destinations: []string{"test2:*"},
					},
				},
			},
		},
		"hostv6cidr": {
			policy: policyv1.ACLPolicy{
				Hosts: policyv1.Hosts{
					"test1": netip.MustParsePrefix("fd7a:115c:a1e0::1/128"),
					"test2": netip.MustParsePrefix("fd7a:115c:a1e0::2/128"),
				},
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"test1"},
						Destinations: []string{"test2:*"},
					},
				},
			},
		},
		"group": {
			policy: policyv1.ACLPolicy{
				Groups: map[string][]string{
					"group:one": {"user1@"},
					"group:two": {"user2@"},
				},
				ACLs: []policyv1.ACL{
					{
						Action:       "accept",
						Sources:      []string{"group:one"},
						Destinations: []string{"group:two:*"},
					},
				},
			},
		},
		// TODO(kradalby): Add similar tests for Tags, might need support
		// in the scenario function when we create or join the clients.
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			scenario := aclScenario(t, &testCase.policy, 1)
			defer scenario.ShutdownAssertNoPanics(t)

			test1ip := netip.MustParseAddr("100.64.0.1")
			test1ip6 := netip.MustParseAddr("fd7a:115c:a1e0::1")
			test1, err := scenario.FindTailscaleClientByIP(test1ip)
			assert.NotNil(t, test1)
			require.NoError(t, err)

			test1fqdn, err := test1.FQDN()
			require.NoError(t, err)
			test1ipURL := fmt.Sprintf("http://%s/etc/hostname", test1ip.String())
			test1ip6URL := fmt.Sprintf("http://[%s]/etc/hostname", test1ip6.String())
			test1fqdnURL := fmt.Sprintf("http://%s/etc/hostname", test1fqdn)

			test2ip := netip.MustParseAddr("100.64.0.2")
			test2ip6 := netip.MustParseAddr("fd7a:115c:a1e0::2")
			test2, err := scenario.FindTailscaleClientByIP(test2ip)
			assert.NotNil(t, test2)
			require.NoError(t, err)

			test2fqdn, err := test2.FQDN()
			require.NoError(t, err)
			test2ipURL := fmt.Sprintf("http://%s/etc/hostname", test2ip.String())
			test2ip6URL := fmt.Sprintf("http://[%s]/etc/hostname", test2ip6.String())
			test2fqdnURL := fmt.Sprintf("http://%s/etc/hostname", test2fqdn)

			// test1 can query test2
			result, err := test1.Curl(test2ipURL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test with URL %s, expected hostname of 13 chars, got %s",
				test2ipURL,
				result,
			)
			require.NoError(t, err)

			result, err = test1.Curl(test2ip6URL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test with URL %s, expected hostname of 13 chars, got %s",
				test2ip6URL,
				result,
			)
			require.NoError(t, err)

			result, err = test1.Curl(test2fqdnURL)
			assert.Lenf(
				t,
				result,
				13,
				"failed to connect from test1 to test with URL %s, expected hostname of 13 chars, got %s",
				test2fqdnURL,
				result,
			)
			require.NoError(t, err)

			result, err = test2.Curl(test1ipURL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test2.Curl(test1ip6URL)
			assert.Empty(t, result)
			require.Error(t, err)

			result, err = test2.Curl(test1fqdnURL)
			assert.Empty(t, result)
			require.Error(t, err)
		})
	}
}

func TestPolicyUpdateWhileRunningWithCLIInDatabase(t *testing.T) {
	IntegrationSkip(t)
	t.Parallel()

	spec := ScenarioSpec{
		NodesPerUser: 1,
		Users:        []string{"user1", "user2"},
	}

	scenario, err := NewScenario(spec)
	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	err = scenario.CreateHeadscaleEnv(
		[]tsic.Option{
			// Alpine containers dont have ip6tables set up, which causes
			// tailscaled to stop configuring the wgengine, causing it
			// to not configure DNS.
			tsic.WithNetfilter("off"),
			tsic.WithDockerEntrypoint([]string{
				"/bin/sh",
				"-c",
				"/bin/sleep 3 ; apk add python3 curl ; update-ca-certificates ; python3 -m http.server --bind :: 80 & tailscaled --tun=tsdev",
			}),
			tsic.WithDockerWorkdir("/"),
		},
		hsic.WithTestName("policyreload"),
		hsic.WithPolicyMode(types.PolicyModeDB),
	)
	require.NoError(t, err)

	_, err = scenario.ListTailscaleClientsFQDNs()
	require.NoError(t, err)

	err = scenario.WaitForTailscaleSync()
	require.NoError(t, err)

	user1Clients, err := scenario.ListTailscaleClients("user1")
	require.NoError(t, err)

	user2Clients, err := scenario.ListTailscaleClients("user2")
	require.NoError(t, err)

	all := append(user1Clients, user2Clients...)

	// Initially all nodes can reach each other
	for _, client := range all {
		for _, peer := range all {
			if client.ContainerID() == peer.ContainerID() {
				continue
			}

			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	headscale, err := scenario.Headscale()
	require.NoError(t, err)

	p := policyv1.ACLPolicy{
		ACLs: []policyv1.ACL{
			{
				Action:       "accept",
				Sources:      []string{"user1@"},
				Destinations: []string{"user2@:*"},
			},
		},
		Hosts: policyv1.Hosts{},
	}

	err = headscale.SetPolicy(&p)
	require.NoError(t, err)

	// Get the current policy and check
	// if it is the same as the one we set.
	var output *policyv1.ACLPolicy
	err = executeAndUnmarshal(
		headscale,
		[]string{
			"headscale",
			"policy",
			"get",
			"--output",
			"json",
		},
		&output,
	)
	require.NoError(t, err)

	assert.Len(t, output.ACLs, 1)

	if diff := cmp.Diff(p, *output); diff != "" {
		t.Errorf("unexpected policy(-want +got):\n%s", diff)
	}

	// Test that user1 can visit all user2
	for _, client := range user1Clients {
		for _, peer := range user2Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Len(t, result, 13)
			require.NoError(t, err)
		}
	}

	// Test that user2 _cannot_ visit user1
	for _, client := range user2Clients {
		for _, peer := range user1Clients {
			fqdn, err := peer.FQDN()
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/etc/hostname", fqdn)
			t.Logf("url from %s to %s", client.Hostname(), url)

			result, err := client.Curl(url)
			assert.Empty(t, result)
			require.Error(t, err)
		}
	}
}
