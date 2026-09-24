package service

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/naive"
)

const naiveTestTLS = `{"security":"tls","tlsSettings":{"certificates":[{"usage":"encipherment","certificate":["CERT"],"key":["KEY"]}]}}`

func TestDesiredNaiveInstancesFiltersDisabledExpiredAndNodeClients(t *testing.T) {
	setupConflictDB(t)
	svc := &InboundService{}
	past := time.Now().Add(-time.Minute).UnixMilli()
	settings := `{"domain":"proxy.example.com","clients":[` +
		`{"email":"alice","password":"one","enable":true},` +
		`{"email":"bob","password":"two","enable":true},` +
		`{"email":"carol","password":"three","enable":false},` +
		`{"email":"expired","password":"four","enable":true,"expiryTime":` +
		strconv.FormatInt(past, 10) + `},` +
		`{"email":"delayed","password":"five","enable":true,"expiryTime":-86400000}]}`
	seedInboundConflict(t, "naive-desired", "", 46401, model.Naive, naiveTestTLS, settings)
	served := loadInboundByTag(t, "naive-desired")
	seedClientTraffic(t, served.Id, "alice", true)
	seedClientTraffic(t, served.Id, "bob", false)
	seedClientTraffic(t, served.Id, "carol", true)
	seedClientTraffic(t, served.Id, "expired", true)
	seedClientTraffic(t, served.Id, "delayed", true)

	seedInboundConflict(t, "naive-depleted", "", 46402, model.Naive, naiveTestTLS,
		`{"domain":"proxy.example.com","clients":[{"email":"dave","password":"six","enable":true}]}`)
	depleted := loadInboundByTag(t, "naive-depleted")
	seedClientTraffic(t, depleted.Id, "dave", false)

	nodeID := 9
	seedInboundConflictNode(t, "naive-node", "", 46403, model.Naive, naiveTestTLS,
		`{"domain":"proxy.example.com","clients":[{"email":"erin","password":"seven","enable":true}]}`, &nodeID)
	seedClientTraffic(t, loadInboundByTag(t, "naive-node").Id, "erin", true)

	instances, err := svc.DesiredNaiveInstances()
	if err != nil {
		t.Fatalf("DesiredNaiveInstances: %v", err)
	}
	if len(instances) != 1 || instances[0].Id != served.Id {
		t.Fatalf("desired instances = %+v, want only inbound %d", instances, served.Id)
	}
	want := []naive.Client{{Email: "alice", Password: "one"}, {Email: "delayed", Password: "five"}}
	if !reflect.DeepEqual(instances[0].Clients, want) {
		t.Fatalf("clients = %+v, want %+v", instances[0].Clients, want)
	}

	built, err := svc.buildInboundForLocalRuntime(database.GetDB(), served)
	if err != nil {
		t.Fatalf("buildInboundForLocalRuntime: %v", err)
	}
	pushed, ok := naive.InstanceFromInbound(built)
	if !ok || !reflect.DeepEqual(pushed.Clients, want) {
		t.Fatalf("interactive apply clients = %+v, want %+v", pushed.Clients, want)
	}
}

func TestUpdateNaiveInboundClientRequiresAndSavesPassword(t *testing.T) {
	setupConflictDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}
	seedInboundConflict(t, "naive-client-edit", "", 46411, model.Naive, naiveTestTLS,
		`{"domain":"proxy.example.com","clients":[{"email":"alice","password":"old","enable":true}]}`)
	inbound := loadInboundByTag(t, "naive-client-edit")

	missingPassword := &model.Inbound{
		Id:       inbound.Id,
		Settings: `{"clients":[{"email":"alice","password":"","enable":true}]}`,
	}
	if _, err := clientSvc.UpdateInboundClient(inboundSvc, missingPassword, "alice"); err == nil || err.Error() != "naive client requires a password\n" {
		t.Fatalf("UpdateInboundClient without password error = %v", err)
	}

	updated := &model.Inbound{
		Id:       inbound.Id,
		Settings: `{"clients":[{"email":"alice","password":"new-secret","enable":true}]}`,
	}
	if _, err := clientSvc.UpdateInboundClient(inboundSvc, updated, "alice"); err != nil {
		t.Fatalf("UpdateInboundClient with password: %v", err)
	}
	stored, err := inboundSvc.GetInbound(inbound.Id)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := inboundSvc.GetClients(stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].Password != "new-secret" {
		t.Fatalf("saved clients = %+v", clients)
	}
}

func TestAddNaiveInboundClientRequiresPassword(t *testing.T) {
	setupConflictDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}
	seedInboundConflict(t, "naive-client-add", "", 46412, model.Naive, naiveTestTLS,
		`{"domain":"proxy.example.com","clients":[]}`)
	inbound := loadInboundByTag(t, "naive-client-add")
	data := &model.Inbound{
		Id:       inbound.Id,
		Settings: `{"clients":[{"email":"alice","password":"","enable":true}]}`,
	}
	if _, err := clientSvc.AddInboundClient(inboundSvc, data); err == nil || err.Error() != "naive client requires a password\n" {
		t.Fatalf("AddInboundClient without password error = %v", err)
	}
}
