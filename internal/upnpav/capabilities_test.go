package upnpav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSonyDigitalImagingSelectsAdvertisedPTPMedia(t *testing.T) {
	for _, enabled := range []string{"Enable", "Disable"} {
		t.Run(enabled, func(t *testing.T) {
			sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/DigitalImagingDesc.xml" {
					fmt.Fprintf(w, `<scpd xmlns="urn:schemas-upnp-org:service-1-0" xmlns:av="urn:schemas-sony-com:av"><serviceStateTable><stateVariable><av:X_DigitalImagingDeviceInfo><av:X_PTP_MediaServerSupport>%s</av:X_PTP_MediaServerSupport></av:X_DigitalImagingDeviceInfo></stateVariable></serviceStateTable></scpd>`, enabled)
					return
				}
				fmt.Fprint(w, strings.Replace(fixtureDescription, "</serviceList>", `<service><serviceType>urn:schemas-sony-com:service:DigitalImaging:1</serviceType><SCPDURL>/DigitalImagingDesc.xml</SCPDURL><controlURL>/upnp/control/DigitalImaging</controlURL></service></serviceList>`, 1))
			}))
			defer sim.Close()
			server, err := Describe(context.Background(), sim.Client(), sim.URL+"/desc.xml")
			if err != nil {
				t.Fatal(err)
			}
			ptp, err := server.PTPMediaSupported(context.Background(), sim.Client())
			if err != nil || ptp != (enabled == "Enable") {
				t.Fatalf("错误选择素材协议：%v %v", ptp, err)
			}
			server.DigitalImagingURL = "http://192.0.2.1/other"
			if _, err := server.PTPMediaSupported(context.Background(), sim.Client()); err == nil {
				t.Fatal("允许跨相机读取能力文档")
			}
		})
	}
}
