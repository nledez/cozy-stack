/* eslint-env mocha*/

var http = require('http')
var Pouchdb = require('pouchdb')

require('longjohn')

var DOCTYPE = 'io.cozy.pouchtestobject'
var SOURCE = 'http://localhost:8080/data/' + DOCTYPE + '/'

function stackRequest (method, path, body, expect, callback) {
  var opts = {
    'method': method,
    'port': process.env.COZY_STACK_PORT || 8080,
    'path': path
  }

  if (body) opts.headers = {'Content-Type': 'application/json'}

  var req = http.request(opts, function (res) {
    var buffers = []
    res.on('error', callback)
    res.on('data', function (chunk) { buffers.push(chunk) })
    res.on('end', function () {
      var resbody = buffers.length === 0 ? ''
        : JSON.parse(Buffer.concat(buffers).toString('utf8'))

      if (res.statusCode !== expect) {
        callback(new Error('expected ' + expect + ', got ' + res.statusCode))
      }
      callback(null, res.statusCode, resbody)
    })
  })

  if (body) req.write(JSON.stringify(body))

  req.end()
}

function asyncForEachKey (obj, iter, done) {
  var n = 0
  Object.keys(obj).forEach(function (key) {
    n++
    iter(obj[key], function (err) {
      if (err) return done && done(err)
      if (--n === 0) return done(null)
    })
  })
}

function createTestObject (value) {
  return function (done) {
    var ctx = this
    ctx.docIds = ctx.docIds || {}
    var doc = {test: value}
    stackRequest('POST', '/data/' + DOCTYPE + '/', doc, 201,
      function (err, status, body) {
        if (err) return done(err)
        ctx.docIds[value] = body
        done(null)
      })
  }
}

function destroySource (done) {
  http.request({
    'method': 'DELETE',
    'port': process.env.COUCH_PORT || 5984,
    'path': '/dev%2Fio-cozy-pouchtestobject'
  }, function (res) {
    var buffers = []
    res.on('error', done)
    res.on('data', function (chunk) { buffers.push(chunk) })
    res.on('end', function () {
      var resbody = buffers.length === 0 ? ''
        : JSON.parse(Buffer.concat(buffers).toString('utf8'))
      console.log('DESTROY DB', res.statusCode, resbody)
      done(null)
    })
  }).end()
}

function destroyObjects (done) {
  var ctx = this
  asyncForEachKey(ctx.docIds, function (doc, next) {
    var path = '/data/' + DOCTYPE + '/' + doc.id + '?rev=' + doc.rev
    stackRequest('DELETE', path, false, 200, next)
  }, done)
}

describe('Replication stack -> pouchdb', function () {
  var target = new Pouchdb('replication-target')
  before(destroySource)
  before(createTestObject(42))
  before(createTestObject(666))
  before(createTestObject(1969))

  it('When I start a replication', function (done) {
    target.replicate.from(SOURCE, {skip_setup: true}, done)
  })

  it('Then I have 3 items in target', function (done) {
    target.get(this.docIds[42].id, function (err, res) {
      if (err) return done()
      if (res['test'] !== 42) done(new Error('wrong doc'))
      done(null)
    })
  })

  after(destroyObjects)
  after(function (done) { target.destroy(done) })
})
