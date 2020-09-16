package tangle

import (
	"bytes"
	"fmt"
	iotago "github.com/iotaledger/iota.go"
	"time"

	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/objectstorage"

	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/gohornet/hornet/pkg/profile"
)

var (
	messagesStorage *objectstorage.ObjectStorage
	metadataStorage *objectstorage.ObjectStorage
)

func MessageCaller(handler interface{}, params ...interface{}) {
	handler.(func(cachedMsg *CachedMessage))(params[0].(*CachedMessage).Retain())
}

func MessageIDCaller(handler interface{}, params ...interface{}) {
	handler.(func(messageID hornet.Hash))(params[0].(hornet.Hash))
}

func NewMessageCaller(handler interface{}, params ...interface{}) {
	handler.(func(cachedMsg *CachedMessage, latestMilestoneIndex milestone.Index, latestSolidMilestoneIndex milestone.Index))(params[0].(*CachedMessage).Retain(), params[1].(milestone.Index), params[2].(milestone.Index))
}

func MessageConfirmedCaller(handler interface{}, params ...interface{}) {
	handler.(func(cachedMeta *CachedMetadata, msIndex milestone.Index, confTime int64))(params[0].(*CachedMetadata).Retain(), params[1].(milestone.Index), params[2].(int64))
}

// CachedMessage contains two cached objects, one for transaction data and one for metadata.
type CachedMessage struct {
	msg      objectstorage.CachedObject
	metadata objectstorage.CachedObject
}

// CachedMetadata contains the cached object only for metadata.
type CachedMetadata struct {
	objectstorage.CachedObject
}

type CachedMessages []*CachedMessage

// msg +1
func (cachedMsgs CachedMessages) Retain() CachedMessages {
	cachedResult := CachedMessages{}
	for _, cachedMsg := range cachedMsgs {
		cachedResult = append(cachedResult, cachedMsg.Retain())
	}
	return cachedResult
}

// msg -1
func (cachedMsgs CachedMessages) Release(force ...bool) {
	for _, cachedTx := range cachedMsgs {
		cachedTx.Release(force...)
	}
}

func (c *CachedMessage) GetMessage() *Message {
	return c.msg.Get().(*Message)
}

// meta +1
func (c *CachedMessage) GetCachedMetadata() *CachedMetadata {
	return &CachedMetadata{c.metadata.Retain()}
}

func (c *CachedMessage) GetMetadata() *hornet.MessageMetadata {
	return c.metadata.Get().(*hornet.MessageMetadata)
}

func (c *CachedMetadata) GetMetadata() *hornet.MessageMetadata {
	return c.Get().(*hornet.MessageMetadata)
}

// msg +1
func (c *CachedMessage) Retain() *CachedMessage {
	return &CachedMessage{
		c.msg.Retain(),
		c.metadata.Retain(),
	}
}

func (c *CachedMetadata) Retain() *CachedMetadata {
	return &CachedMetadata{c.CachedObject.Retain()}
}

func (c *CachedMessage) Exists() bool {
	return c.msg.Exists()
}

// msg -1
// meta -1
func (c *CachedMessage) ConsumeMessageAndMetadata(consumer func(*Message, *hornet.MessageMetadata)) {

	c.msg.Consume(func(txObject objectstorage.StorableObject) {
		c.metadata.Consume(func(metadataObject objectstorage.StorableObject) {
			consumer(txObject.(*Message), metadataObject.(*hornet.MessageMetadata))
		}, true)
	}, true)
}

// msg -1
// meta -1
func (c *CachedMessage) ConsumeMessage(consumer func(*Message)) {
	defer c.metadata.Release(true)
	c.msg.Consume(func(object objectstorage.StorableObject) {
		consumer(object.(*Message))
	}, true)
}

// msg -1
// meta -1
func (c *CachedMessage) ConsumeMetadata(consumer func(*hornet.MessageMetadata)) {
	defer c.msg.Release(true)
	c.metadata.Consume(func(object objectstorage.StorableObject) {
		consumer(object.(*hornet.MessageMetadata))
	}, true)
}

// meta -1
func (c *CachedMetadata) ConsumeMetadata(consumer func(*hornet.MessageMetadata)) {
	c.Consume(func(object objectstorage.StorableObject) {
		consumer(object.(*hornet.MessageMetadata))
	}, true)
}

// msg -1
func (c *CachedMessage) Release(force ...bool) {
	c.msg.Release(force...)
	c.metadata.Release(force...)
}

func messageFactory(key []byte, data []byte) (objectstorage.StorableObject, error) {
	msg := &Message{
		messageID: key[:32],
		message:   &iotago.Message{},
	}

	if _, err := msg.message.Deserialize(data, iotago.DeSeriModeNoValidation); err != nil {
		return nil, err
	}

	return msg, nil
}

func GetTransactionStorageSize() int {
	return messagesStorage.GetSize()
}

func configureMessageStorage(store kvstore.KVStore, opts profile.CacheOpts) {

	messagesStorage = objectstorage.New(
		store.WithRealm([]byte{StorePrefixMessages}),
		messageFactory,
		objectstorage.CacheTime(time.Duration(opts.CacheTimeMs)*time.Millisecond),
		objectstorage.PersistenceEnabled(true),
		objectstorage.StoreOnCreation(true),
		objectstorage.LeakDetectionEnabled(opts.LeakDetectionOptions.Enabled,
			objectstorage.LeakDetectionOptions{
				MaxConsumersPerObject: opts.LeakDetectionOptions.MaxConsumersPerObject,
				MaxConsumerHoldTime:   time.Duration(opts.LeakDetectionOptions.MaxConsumerHoldTimeSec) * time.Second,
			}),
	)

	metadataStorage = objectstorage.New(
		store.WithRealm([]byte{StorePrefixMessageMetadata}),
		hornet.MetadataFactory,
		objectstorage.CacheTime(time.Duration(opts.CacheTimeMs)*time.Millisecond),
		objectstorage.PersistenceEnabled(true),
		objectstorage.StoreOnCreation(false),
		objectstorage.LeakDetectionEnabled(opts.LeakDetectionOptions.Enabled,
			objectstorage.LeakDetectionOptions{
				MaxConsumersPerObject: opts.LeakDetectionOptions.MaxConsumersPerObject,
				MaxConsumerHoldTime:   time.Duration(opts.LeakDetectionOptions.MaxConsumerHoldTimeSec) * time.Second,
			}),
	)
}

// msg +1
func GetCachedMessageOrNil(messageID hornet.Hash) *CachedMessage {
	cachedMsg := messagesStorage.Load(messageID) // msg +1
	if !cachedMsg.Exists() {
		cachedMsg.Release(true) // msg -1
		return nil
	}

	cachedMeta := metadataStorage.Load(messageID) // meta +1
	if !cachedMeta.Exists() {
		cachedMsg.Release(true)  // msg -1
		cachedMeta.Release(true) // meta -1
		return nil
	}

	return &CachedMessage{
		msg:      cachedMsg,
		metadata: cachedMeta,
	}
}

// metadata +1
func GetCachedMessageMetadataOrNil(messageID hornet.Hash) *CachedMetadata {
	cachedMeta := metadataStorage.Load(messageID) // meta +1
	if !cachedMeta.Exists() {
		cachedMeta.Release(true) // metadata -1
		return nil
	}
	return &CachedMetadata{CachedObject: cachedMeta}
}

// GetStoredMetadataOrNil returns a metadata object without accessing the cache layer.
func GetStoredMetadataOrNil(txHash hornet.Hash) *hornet.MessageMetadata {
	storedMeta := metadataStorage.LoadObjectFromStore(txHash)
	if storedMeta == nil {
		return nil
	}
	return storedMeta.(*hornet.MessageMetadata)
}

// ContainsMessage returns if the given transaction exists in the cache/persistence layer.
func ContainsMessage(messageID hornet.Hash) bool {
	return messagesStorage.Contains(messageID)
}

// MessageExistsInStore returns if the given transaction exists in the persistence layer.
func MessageExistsInStore(messageID hornet.Hash) bool {
	return messagesStorage.ObjectExistsInStore(messageID)
}

// msg +1
func StoreMessageIfAbsent(message *iotago.Message) (cachedMsg *CachedMessage, newlyAdded bool) {

	// Store msg + metadata atomically in the same callback
	var cachedMeta objectstorage.CachedObject

	hash, err := message.Hash()
	if err != nil {
		//TODO: check where to do this better
		panic(err)
	}

	messageID := hash[:]

	msg := &Message{
		messageID: messageID,
		message:   message,
	}

	cachedTxData := messagesStorage.ComputeIfAbsent(msg.ObjectStorageKey(), func(key []byte) objectstorage.StorableObject { // msg +1
		newlyAdded = true

		metadata := hornet.NewMessageMetadata(messageID, message.Parent1[:], message.Parent2[:])
		cachedMeta = metadataStorage.Store(metadata) // meta +1

		msg.Persist()
		msg.SetModified()
		return msg
	})

	// if we didn't create a new entry - retrieve the corresponding metadata (it should always exist since it gets created atomically)
	if !newlyAdded {
		cachedMeta = metadataStorage.Load(messageID) // meta +1
	}

	return &CachedMessage{msg: cachedTxData, metadata: cachedMeta}, newlyAdded
}

// MessageIDConsumer consumes the given message ID during looping through all messages in the persistence layer.
type MessageIDConsumer func(messageID hornet.Hash) bool

// ForEachMessageID loops over all message IDs.
func ForEachMessageID(consumer MessageIDConsumer, skipCache bool) {
	messagesStorage.ForEachKeyOnly(func(messageID []byte) bool {
		return consumer(messageID)
	}, skipCache)
}

// ForEachMessageMetadataMessageID loops over all message metadata message IDs.
func ForEachMessageMetadataMessageID(consumer MessageIDConsumer, skipCache bool) {
	metadataStorage.ForEachKeyOnly(func(messageID []byte) bool {
		return consumer(messageID)
	}, skipCache)
}

// DeleteMessage deletes the message and metadata in the cache/persistence layer.
func DeleteMessage(messageID hornet.Hash) {
	// metadata has to be deleted before the msg, otherwise we could run into a data race in the object storage
	metadataStorage.Delete(messageID)
	messagesStorage.Delete(messageID)
}

// DeleteMessageMetadata deletes the metadata in the cache/persistence layer.
func DeleteMessageMetadata(messageID hornet.Hash) {
	metadataStorage.Delete(messageID)
}

func ShutdownMessagesStorage() {
	messagesStorage.Shutdown()
	metadataStorage.Shutdown()
}

func FlushMessagesStorage() {
	messagesStorage.Flush()
	metadataStorage.Flush()
}

// msg +1
func AddMessageToStorage(message *iotago.Message, latestMilestoneIndex milestone.Index, requested bool, forceRelease bool, reapply bool) (cachedMessage *CachedMessage, alreadyAdded bool) {

	cachedMessage, isNew := StoreMessageIfAbsent(message) // msg +1
	if !isNew && !reapply {
		return cachedMessage, true
	}

	StoreChild(cachedMessage.GetMessage().GetParent1MessageID(), cachedMessage.GetMessage().GetMessageID()).Release(forceRelease)
	if !bytes.Equal(cachedMessage.GetMessage().GetParent1MessageID(), cachedMessage.GetMessage().GetParent2MessageID()) {
		StoreChild(cachedMessage.GetMessage().GetParent2MessageID(), cachedMessage.GetMessage().GetMessageID()).Release(forceRelease)
	}

	// Store only non-requested messages, since all requested messages are confirmed by a milestone anyway
	// This is only used to delete unconfirmed messages from the database at pruning
	if !requested {
		StoreUnconfirmedMessage(latestMilestoneIndex, cachedMessage.GetMessage().GetMessageID()).Release(true)
	}

	ms, err := CheckIfMilestone(message)
	if err != nil {
		// Invalid milestone
		Events.ReceivedInvalidMilestone.Trigger(fmt.Errorf("invalid milestone detected! Err: %w", err))
	}

	if ms != nil {

		cachedMilestone := storeMilestone(milestone.Index(ms.Index), cachedMessage.GetMessage().GetMessageID())

		Events.ReceivedValidMilestone.Trigger(cachedMilestone) // milestone pass +1

		// Force release to store milestones without caching
		cachedMilestone.Release(true) // milestone +-0
	}

	return cachedMessage, false
}
