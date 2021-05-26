package vm

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/filecoin-project/specs-actors/v5/actors/builtin"
	"github.com/filecoin-project/specs-actors/v5/actors/util/adt"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-blockservice"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	offline "github.com/ipfs/go-ipfs-exchange-offline"
	"github.com/ipfs/go-merkledag"

	"github.com/ipfs/go-cid"
	format "github.com/ipfs/go-ipld-format"
	"github.com/ipld/go-car"
	cbg "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"
)

// Update this when generating new vectors for a new filecoin network version
const defaultNetworkName = "hyperdrive"

// option functions for setting vector generator field cleanly
// write state tree to encoded car file
// deal with top of state tree not matching filecoin protocol
// persisting runtime values

type TestVector struct {
	ID string

	StartState     []byte
	StartStateTree cid.Cid
	Message        *ChainMessage

	Receipt      MessageResult
	EndStateTree cid.Cid

	// Runtime values
	Epoch      abi.ChainEpoch
	Version    network.Version
	CircSupply abi.TokenAmount
}

func (tv *TestVector) MarshalJSON() ([]byte, error) {
	tvs, err := newTestVectorSerial(tv)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&tvs)
}

type Option func(tv *TestVector) error

func SetID(id string) Option {
	return func(tv *TestVector) error {
		tv.ID = id
		return nil
	}
}

func SetState(rawRoot cid.Cid, store adt.Store) Option {
	return func(tv *TestVector) error {
		root, err := flushTreeTopLevel(context.Background(), store, rawRoot)
		if err != nil {
			return err
		}
		dserv := dagServiceFromStore(store)
		carBytes, err := encodeCAR(dserv, root)
		if err != nil {
			return err
		}
		tv.StartState = carBytes
		return nil
	}
}

func SetEpoch(e abi.ChainEpoch) Option {
	return func(tv *TestVector) error {
		tv.Epoch = e
		return nil
	}
}

func SetNetworkVersion(nv network.Version) Option {
	return func(tv *TestVector) error {
		tv.Version = nv
		return nil
	}
}

func SetCircSupply(circSupply big.Int) Option {
	return func(tv *TestVector) error {
		tv.CircSupply = circSupply
		return nil
	}
}

func SetStartStateTree(rawRoot cid.Cid, store adt.Store) Option {
	return func(tv *TestVector) error {
		fmt.Printf("raw root: %s\n", rawRoot)
		root, err := flushTreeTopLevel(context.Background(), store, rawRoot)
		if err != nil {
			return err
		}
		tv.StartStateTree = root
		return nil
	}
}

func SetEndStateTree(rawRoot cid.Cid, store adt.Store) Option {
	return func(tv *TestVector) error {
		root, err := flushTreeTopLevel(context.Background(), store, rawRoot)
		if err != nil {
			return err
		}
		tv.EndStateTree = root
		return nil
	}
}

func SetMessage(from, to address.Address, nonce uint64, value big.Int, method abi.MethodNum, params interface{}) Option {
	return func(tv *TestVector) error {
		msg, err := makeChainMessage(from, to, nonce, value, method, params)
		if err != nil {
			return err
		}
		tv.Message = msg
		return nil
	}
}

func SetReceipt(res MessageResult) Option {
	return func(tv *TestVector) error {
		tv.Receipt = res
		return nil
	}
}

func StartConditions(v *VM, id string) []Option {
	var opts []Option
	opts = append(opts, SetEpoch(v.GetEpoch()))
	opts = append(opts, SetCircSupply(v.GetCirculatingSupply()))
	opts = append(opts, SetNetworkVersion(v.networkVersion))
	opts = append(opts, SetStartStateTree(v.StateRoot(), v.store))
	opts = append(opts, SetState(v.StateRoot(), v.store))
	opts = append(opts, SetID(id))

	return opts
}

//
// Internal types for serialization
// Taken from https://github.com/filecoin-project/test-vectors/blob/master/schema/schema.go
//

type generationData struct {
	Source string `json:"source"`
}

type metadata struct {
	ID  string           `json:"id"`
	Gen []generationData `json:"gen"`
}

type variant struct {
	// ID of the variant, usually the codename of the upgrade.
	ID             string `json:"id"`
	Epoch          int64  `json:"epoch"`
	NetworkVersion uint   `json:"nv"`
}

type preconditions struct {
	Variants   []variant        `json:"variants"`
	StateTree  *stateTreeSerial `json:"state_tree,omitempty"`
	BaseFee    *big.Int         `json:"basefee,omitempty"`
	CircSupply *big.Int         `json:"circ_supply,omitempty"`
}

type base64EncodedBytes []byte

func (b base64EncodedBytes) String() string {
	return base64.StdEncoding.EncodeToString(b)
}

// MarshalJSON implements json.Marshal for Base64EncodedBytes
func (b base64EncodedBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.String())
}

type messageSerial struct {
	Bytes base64EncodedBytes `json:"bytes"`
}
type stateTreeSerial struct {
	RootCID cid.Cid `json:"root_cid"`
}

// Receipt represents a receipt to match against.
type receiptSerial struct {
	// ExitCode must be interpreted by the driver as an exitcode.ExitCode
	// in Lotus, or equivalent type in other implementations.
	ExitCode    int64              `json:"exit_code"`
	ReturnValue base64EncodedBytes `json:"return"`
	GasUsed     int64              `json:"gas_used"`
}

// Postconditions contain a representation of VM state at th end of the test
type postconditions struct {
	StateTree *stateTreeSerial `json:"state_tree"`
	Receipts  []*receiptSerial `json:"receipts"`
}

type testVectorSerial struct {
	Class string `json:"class"`

	Meta *metadata `json:"_meta"`

	// CAR binary data to be loaded into the test environment. Should
	// contain objects of entire state tree
	CAR base64EncodedBytes `json:"car"`

	Pre *preconditions `json:"preconditions"`

	ApplyMessages []messageSerial `json:"apply_messages,omitempty"`

	Post *postconditions `json:"postconditions"`
}

func newTestVectorSerial(tv *TestVector) (*testVectorSerial, error) {
	zero := big.Zero()
	circSupply := tv.CircSupply
	var buf bytes.Buffer
	if err := tv.Message.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	msgBytes := buf.Bytes()
	if err := tv.Receipt.Ret.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	retBytes := buf.Bytes()

	return &testVectorSerial{
		Class: "message",
		Meta: &metadata{
			ID: tv.ID,
			Gen: []generationData{
				{Source: "specs-actors_test_auto_gen"},
			},
		},
		CAR: tv.StartState,
		Pre: &preconditions{
			Variants: []variant{
				{ID: defaultNetworkName, Epoch: int64(tv.Epoch), NetworkVersion: uint(tv.Version)},
			},
			StateTree:  &stateTreeSerial{RootCID: tv.StartStateTree},
			BaseFee:    &zero,
			CircSupply: &circSupply,
		},
		ApplyMessages: []messageSerial{
			{Bytes: msgBytes},
		},
		Post: &postconditions{
			StateTree: &stateTreeSerial{RootCID: tv.EndStateTree},
			Receipts: []*receiptSerial{
				{
					ExitCode:    int64(tv.Receipt.Code),
					ReturnValue: retBytes,
					GasUsed:     tv.Receipt.GasCharged,
				},
			},
		},
	}, nil
}

// encodeCAR taken from https://github.com/filecoin-project/test-vectors/blob/master/gen/builders/car.go#L16
func encodeCAR(dagserv format.DAGService, roots ...cid.Cid) ([]byte, error) {
	carWalkFn := func(nd format.Node) (out []*format.Link, err error) {
		//fmt.Printf("%s: %x\n", nd.Cid(), nd.RawData())
		for _, link := range nd.Links() {
			// skip sector cids
			if link.Cid.Prefix().Codec == cid.FilCommitmentSealed || link.Cid.Prefix().Codec == cid.FilCommitmentUnsealed {
				continue
			}
			// skip builtin actor cids
			if builtin.IsBuiltinActor(link.Cid) {
				continue
			}
			out = append(out, link)
		}
		return out, nil
	}

	var (
		out = new(bytes.Buffer)
		gw  = gzip.NewWriter(out)
	)
	fmt.Printf("roots len: %d, roots[0]: %s\n", len(roots), roots[0])
	if err := car.WriteCarWithWalker(context.Background(), dagserv, roots, gw, carWalkFn); err != nil {
		return nil, err
	}
	if err := gw.Flush(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

func dagServiceFromStore(store adt.Store) format.DAGService {
	bs := &adtBlockStoreForDAGService{store: store}
	offl := offline.Exchange(bs)
	blkserv := blockservice.New(bs, offl)
	return merkledag.NewDAGService(blkserv)
}

type adtBlockStoreForDAGService struct {
	store adt.Store
}

var _ blockstore.Blockstore = (*adtBlockStoreForDAGService)(nil)

func (a *adtBlockStoreForDAGService) DeleteBlock(c cid.Cid) error {
	return xerrors.Errorf("cannot delete cid: %s, unsupported operation\n", c)
}

func (a *adtBlockStoreForDAGService) Has(c cid.Cid) (bool, error) {
	// All errors will be treated as NotFound
	_, err := a.Get(c)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (a *adtBlockStoreForDAGService) Get(c cid.Cid) (blocks.Block, error) {
	d := cbg.Deferred{}
	if err := a.store.Get(context.Background(), c, &d); err != nil {
		return nil, err
	}
	return blocks.NewBlockWithCid(d.Raw, c)
}

func (a *adtBlockStoreForDAGService) GetSize(c cid.Cid) (int, error) {
	d := cbg.Deferred{}
	if err := a.store.Get(context.Background(), c, &d); err != nil {
		return 0, err
	}
	return len(d.Raw), nil
}

func (a *adtBlockStoreForDAGService) Put(b blocks.Block) error {
	d := cbg.Deferred{
		Raw: b.RawData(),
	}
	_, err := a.store.Put(context.Background(), d)
	return err
}

func (a *adtBlockStoreForDAGService) PutMany(bs []blocks.Block) error {
	for _, b := range bs {
		if err := a.Put(b); err != nil {
			return err
		}
	}
	return nil
}

func (a *adtBlockStoreForDAGService) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	return nil, xerrors.Errorf("unsupported operation")
}

// not supported -- noop
func (a *adtBlockStoreForDAGService) HashOnRead(enabled bool) {}

// Top level state tree

const CurrentStateTreeVersion = 3

type StateTreeVersion uint64

type StateRoot struct {
	// State tree version.
	Version StateTreeVersion
	// Actors tree. The structure depends on the state root version.
	Actors cid.Cid
	// Info. The structure depends on the state root version.
	Info cid.Cid
}

type StateInfo0 struct{}

// Write top level object of state tree
func flushTreeTopLevel(ctx context.Context, store adt.Store, rawRoot cid.Cid) (cid.Cid, error) {
	infoCid, err := store.Put(ctx, new(StateInfo0))
	if err != nil {
		return cid.Undef, err
	}
	top := &StateRoot{
		Version: CurrentStateTreeVersion,
		Actors:  rawRoot,
		Info:    infoCid,
	}
	return store.Put(ctx, top)
}
